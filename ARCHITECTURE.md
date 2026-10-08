# Arquitetura

Este documento registra as decisões técnicas da solução. Cada seção é preenchida no grupo de tasks de `openspec/changes/wallet-wagering-service/tasks.md` em que a decisão é tomada; seções ainda vazias estão marcadas como pendentes.

## 1. Dinheiro

Implementado em `internal/domain/money`. O enunciado trata do assunto na seção 6.1.

### 1.1. Representação

`Money` guarda um `int64` com a quantidade de unidades mínimas (centavos) e uma `Currency`. A escala é fixa em duas casas: `"25.00"` vira `2500` e `"0.05"` vira `5`. Os dois campos são privados e todo método devolve um valor novo, então um `Money` nunca muda depois de criado.

Nenhuma etapa usa ponto flutuante. O texto de entrada é validado caractere a caractere e convertido com `strconv.ParseInt`, que só trabalha com inteiros; a saída é montada com divisão e resto por 100. `make nofloat` falha se `float32`, `float64`, `ParseFloat` ou `big.Float` aparecer em `internal/domain` ou `internal/app`.

- **Por quê:** todo valor aceito é um inteiro exato, e soma, subtração, negação e comparação viram operações de inteiros.
- **Alternativa descartada:** uma biblioteca decimal. O serviço não multiplica, não divide e não arredonda, e a validação de formato da seção 1.3 teria de ser escrita de qualquer modo, porque os parsers dessas bibliotecas aceitam formas como `"25"` e `"1e2"`.
- **Base:** "Use `int64` em unidades mínimas ou uma biblioteca decimal de precisão exata." A seção 14 torna eliminatório o "cálculo monetário em ponto flutuante".

### 1.2. Limites e overflow

| Limite | Unidades mínimas | Forma decimal |
| --- | --- | --- |
| Maior valor | `9223372036854775807` | `92233720368547758.07` |
| Menor valor | `-9223372036854775808` | `-92233720368547758.08` |

`Parse`, `Add`, `Sub` e `Neg` devolvem `ErrOverflow`. No `Parse` quem acusa o limite é o `strconv.ParseInt`. Nas outras três a checagem vem antes da conta, porque a aritmética de `int64` em Go dá a volta em silêncio. `Neg` do menor valor é overflow, pois o oposto dele não cabe em `int64`.

- **Base:** "Se utilizar `int64`, trate overflow no parsing, na soma, na subtração e na negação." e "Documente a representação e seus limites."

### 1.3. Formato estrito na entrada

`Parse` aceita somente `^(0|[1-9][0-9]*)\.[0-9]{2}$`. Não há arredondamento nem normalização.

| Entrada | Resultado |
| --- | --- |
| `"25.00"`, `"0.00"`, `"0.05"` | aceita |
| `"25"`, `"25.0"`, `"25."`, `".50"`, `"025.00"`, `"+25.00"`, `"25,00"` | `ErrInvalidAmount` |
| `"25.000"`, `"25.001"` | `ErrInvalidAmount` |
| `""`, `" 25.00"`, `"NaN"`, `"Infinity"`, `"1e2"` | `ErrInvalidAmount` |
| `"-25.00"`, `"-0.00"` | `ErrNegativeAmount` |
| `"92233720368547758.08"` | `ErrOverflow` |

Valores negativos só existem em cálculos internos, criados por `FromMinorUnits` ou por `Sub` e `Neg`.

- **Por quê:** com uma única forma textual por valor não há normalização a fazer antes do hash de idempotência, e HTTP e SQS aceitam exatamente as mesmas entradas.
- **Alternativa descartada:** aceitar formas equivalentes (`"25"`, `"25.0"`) e normalizar. Seria mais uma regra a documentar e testar.
- **Base:** "Rejeite valores vazios, `NaN`, `Infinity`, notação científica, escala excedente e valores negativos nas entradas financeiras externas." e "Não arredonde silenciosamente uma entrada inválida. Caso aceite formas equivalentes, documente a normalização anterior ao hash de idempotência." O enunciado permite as duas vias; o formato estrito é escolha deste projeto.

### 1.4. Moedas

`ParseCurrency` aceita `BRL`, `USD` e `EUR`, em maiúsculas. Qualquer outro código devolve `ErrInvalidCurrency`. `Currency` tem o campo privado, então fora do pacote só existe moeda validada.

- **Por quê:** as três têm expoente 2 na ISO 4217, igual à escala fixa. Moedas como `JPY` (expoente 0) e `BHD` (expoente 3) seriam representadas errado com duas casas.
- **Base:** "Use escala fixa de duas casas e código de moeda ISO 4217." A lista fechada é interpretação deste projeto; o enunciado não limita as moedas.

### 1.5. Erros e valor não inicializado

Os erros são sentinelas, comparáveis com `errors.Is`:

| Erro | Quando |
| --- | --- |
| `ErrInvalidAmount` | texto fora do formato, ou objeto JSON malformado |
| `ErrNegativeAmount` | valor negativo em entrada externa |
| `ErrInvalidCurrency` | moeda fora da lista |
| `ErrCurrencyMismatch` | soma, subtração ou comparação entre moedas diferentes |
| `ErrOverflow` | resultado fora dos limites de `int64` |
| `ErrUninitialized` | operação sobre `Money{}` ou `Currency{}` |

`Money{}` é inválido. `Add`, `Sub`, `Neg`, `Cmp` e `MarshalJSON` devolvem `ErrUninitialized`. `Equal`, `IsZero`, `IsPositive` e `IsNegative` devolvem `false`. As leituras `MinorUnits`, `Currency` e `Amount` não devolvem erro. Essa divisão entre operações que falham e leituras que não falham é interpretação deste projeto.

- **Base:** "Aritmética e comparação de valores monetários exigem moedas compatíveis." e, na seção 6, "Erros de domínio devem ser classificáveis por tipo ou `errors.Is`/`errors.As`."

### 1.6. JSON

A escrita produz `{"amount":"25.00","currency":"BRL"}`, com `amount` sempre em string. Na leitura, `amount` precisa ser string, os dois campos são obrigatórios e um campo desconhecido é recusado. Limitação conhecida: a leitura usa o decodificador padrão do Go, que aceita o nome do campo em outra caixa (`AMOUNT`) e, em campo repetido, fica com a última ocorrência. O enunciado não trata desses casos, e eles não afetam a idempotência, cujo hash é calculado sobre os valores já interpretados. Um `amount` numérico é recusado na decodificação, sem ser convertido em número. A leitura usa `ParseCurrency` e `Parse`, as mesmas funções das demais entradas.

- **Base:** "O contrato externo recebe e devolve valores como `{"amount":"25.00","currency":"BRL"}`."

### 1.7. Persistência

O valor será gravado como `BIGINT` de unidades mínimas e a moeda como `CHAR(3)`, sem conversão entre o tipo do domínio e o do banco. Ainda não implementado: as migrations e os repositórios são do grupo 9 do `tasks.md`.

- **Base:** "A persistência deve preservar exatamente valor e moeda, por exemplo com unidades mínimas em `BIGINT` ou decimal em `NUMERIC`."

## 2. Acesso ao banco e delimitação de transações

_Pendente (grupo 9 do `tasks.md`)._

## 3. Controle de concorrência e locks

_Pendente (grupo 11 do `tasks.md`)._

## 4. Idempotência

_Pendente (grupo 11 do `tasks.md`)._

## 5. Máquina de estados da transação

_Pendente (grupo 6 do `tasks.md`)._

## 6. Reversões (`REFUND` e `ROLLBACK`)

_Pendente (grupo 6 do `tasks.md`)._

## 7. Referências pendentes

_Pendente (grupos 11 e 16 do `tasks.md`)._

## 8. Inbox e consumidor SQS

_Pendente (grupo 15 do `tasks.md`)._

## 9. Outbox e publicação de eventos

_Pendente (grupo 16 do `tasks.md`)._

## 10. Autenticação e autorização

_Pendente (grupo 13 do `tasks.md`)._

## 11. Composição com Fx e shutdown

_Pendente (grupo 17 do `tasks.md`)._

## 12. Contrato HTTP: códigos e corpos de erro

_Pendente (grupo 14 do `tasks.md`)._

## 13. Códigos de falha (`failureCode`)

Os códigos estão definidos em `internal/domain/failure`, como um conjunto fechado. O enunciado trata do assunto na seção 7.

### 13.1. Entrada inválida e rejeição

Há dois grupos de códigos, separados por uma pergunta: dá para julgar olhando só a requisição?

- **Entrada inválida:** sim. Nada é gravado, e quem enviou pode corrigir e reenviar.
- **Rejeição de negócio:** não, o julgamento depende do estado gravado, com a carteira existente. A transação é gravada como `REJECTED` com o código, e o resultado é definitivo.

No código são dois tipos de erro, `failure.InvalidInputError` e `failure.RejectionError`, cada um com o `Code`. Quem chama usa `errors.As` para saber qual dos dois recebeu.

- **Por quê gravar divergência de jogador e de moeda como rejeição:** quem envia pela fila não recebe resposta; a transação rejeitada e seu evento são o único aviso.
- **Base:** "Toda rejeição deve fornecer um `failureCode` estável e documentado, distinguindo entradas corrigíveis de resultados definitivos." A linha divisória e os nomes dos códigos são interpretação deste projeto.

### 13.2. Códigos de entrada inválida

| Código | Situação |
| --- | --- |
| `MALFORMED_REQUEST` | JSON inválido, campo ausente ou desconhecido |
| `INVALID_IDENTIFIER` | identificador malformado |
| `INVALID_MONEY` | valor ou moeda fora do formato |
| `MISSING_IDEMPOTENCY_KEY` | chave de idempotência ausente ou vazia |
| `UNSUPPORTED_KIND` | `OPENING` ou tipo desconhecido |
| `INVALID_AMOUNT_FOR_KIND` | valor não permitido para o tipo da operação |
| `MISSING_REFERENCE` | `REFUND` ou `ROLLBACK` sem referência |
| `UNEXPECTED_REFERENCE` | `BET` ou `LOSS` com referência |
| `INVALID_CURSOR` | cursor do ledger inválido |
| `WALLET_NOT_FOUND` | carteira inexistente |
| `TRANSACTION_NOT_FOUND` | transação inexistente ou de outro provedor |

### 13.3. Códigos de rejeição

| Código | Situação |
| --- | --- |
| `INSUFFICIENT_FUNDS` | `BET` maior que o saldo |
| `REVERSAL_INSUFFICIENT_FUNDS` | reversão que debitaria além do saldo |
| `BALANCE_OVERFLOW` | crédito que estouraria o limite de `int64` |
| `PLAYER_WALLET_MISMATCH` | o jogador não é o dono da carteira |
| `CURRENCY_MISMATCH` | moeda diferente da carteira |
| `REFERENCE_NOT_FOUND` | a referência não chegou dentro do prazo |
| `REFERENCE_NOT_PROCESSED` | a referência está `REJECTED` ou `FAILED` |
| `REFERENCE_MISMATCH` | jogador, carteira, moeda, rodada ou valor divergem da referência |
| `REFERENCE_KIND_NOT_ALLOWED` | o tipo da referência não pode ser desfeito por esta operação |
| `REFERENCE_ALREADY_REVERSED` | a referência já tem uma reversão processada |

`INSUFFICIENT_FUNDS` e `REVERSAL_INSUFFICIENT_FUNDS` são distintos por exigência do enunciado: "Uma reversão que precisaria debitar mais que o saldo disponível deve ser rejeitada e auditável. Seu código de falha deve ser diferente daquele usado para uma aposta sem saldo."

### 13.4. Falha

`PROCESSING_FAILED` marca a transação `FAILED`, uma falha permanente de infraestrutura. Não é rejeição de negócio.

### 13.5. O que ainda não está implementado

Este grupo entrega só os códigos e os tipos de erro. As regras que produzem cada código são dos grupos 5, 6 e 11 do `tasks.md`; a tradução para status HTTP e para a DLQ é dos grupos 14 e 15.

## 14. Observabilidade

_Pendente (grupo 12 do `tasks.md`)._

## 15. Limitações, interpretações adotadas e trabalho não concluído

_Pendente (grupo 19 do `tasks.md`)._

## 16. Identificadores

Implementado em `internal/domain/ids`. O enunciado mostra `playerId` e `walletId` como UUID e `roundId` e `gameId` como texto livre (seção 9), sem fixar formato nem limite. As regras abaixo são interpretação deste projeto.

### 16.1. Um tipo por identificador

Há cinco tipos de UUID (`WalletID`, `PlayerID`, `TransactionID`, `EntryID`, `EventID`) e cinco de texto (`ProviderID`, `ExternalTransactionID`, `RoundID`, `GameID`, `IdempotencyKey`). Cada um só é criado por sua função `Parse` ou `New`.

- **Por quê:** o compilador recusa passar um `PlayerID` onde se espera um `WalletID`. Trocar os dois num serviço de carteira movimenta o dinheiro da pessoa errada.
- **Alternativa descartada:** um tipo só para todos os UUID e outro para todos os textos. Seriam cerca de cem linhas a menos, e a troca de argumentos passaria a depender só de testes.

### 16.2. UUID

Os ids criados pelo serviço são UUIDv7, gerados na aplicação: carteira, transação, lançamento e evento. `PlayerID` não tem gerador, pois o jogador vem sempre de fora. O UUID todo zerado é recusado, porque é o valor de um identificador não inicializado.

- **Por quê UUIDv7:** começa pelo instante de criação, então ids novos caem no fim do índice do banco.
- **Limitação conhecida:** a leitura usa `uuid.Parse`, que aceita formas além da canônica (maiúsculas, sem hífens, entre chaves). Não há efeito prático: o serviço compara, grava e devolve sempre a forma canônica.

### 16.3. Texto

Um identificador de texto não pode ser vazio, não pode ter espaço nas pontas, tem até 255 caracteres e precisa ser UTF-8 válido, sem o byte nulo. As duas últimas regras existem porque o PostgreSQL recusa esses bytes numa coluna de texto; sem elas, uma entrada que o cliente pode corrigir viraria erro interno.

- **Limitação conhecida:** não há restrição de alfabeto. Uma quebra de linha no meio do texto é aceita.

### 16.4. Erros e serialização

As funções `Parse` devolvem um único erro, `ids.ErrInvalid`. A tradução para `INVALID_IDENTIFIER` ou `MISSING_IDEMPOTENCY_KEY` é da camada de aplicação, porque `ids` não importa `failure`. Os tipos implementam `encoding.TextMarshaler`, então saem como texto em JSON e em logs.

## 17. Lançamento do ledger

Implementado em `internal/domain/ledger`. O enunciado trata do assunto na seção 6.4. A tabela, os índices únicos e os triggers que impedem alteração são do grupo 9 do `tasks.md` e ainda não existem.

### 17.1. Conteúdo

Um lançamento tem identificador, carteira, transação, direção (`DEBIT` ou `CREDIT`), valor, saldo anterior, saldo posterior, versão da carteira e instante de criação. Os campos são privados e não há método que os altere. `Fields()` devolve uma cópia dos dados, então mudar o que foi devolvido não muda o lançamento.

- **Acréscimo ao enunciado:** a versão da carteira. O enunciado lista os outros oito campos; a versão existe para ordenar o ledger e ligar o lançamento ao evento de mudança de saldo.
- **Base:** "Cada lançamento registra `id`, `walletId`, `transactionId`, direção (`DEBIT` ou `CREDIT`), valor, saldo anterior, saldo posterior e instante de criação." e "O lançamento é imutável".

### 17.2. Validação

A construção recusa: carteira ou transação vazias, versão menor que 1, valor que não seja maior que zero, saldo anterior ou posterior negativo, e saldo posterior diferente de `anterior + valor` no crédito ou `anterior - valor` no débito. Moedas diferentes, valor não inicializado e overflow são recusados pela própria conta, feita com `Money`.

Todos os casos devolvem o mesmo erro, `ledger.ErrInvalidEntry`. Um lançamento inválido não é rejeição de negócio: indica defeito no código que o montou ou dado corrompido, e ninguém precisa distinguir qual regra falhou.

- **Base:** "sua construção deve validar `balanceAfter = balanceBefore ± money`, conforme a direção" e, na seção 6, "Valores de domínio não inicializados ou inválidos devem ser rejeitados."

### 17.3. Criação e reidratação

`NewEntry` gera o identificador (UUIDv7) e marca o instante atual em UTC, com precisão de microssegundo, a mesma do PostgreSQL. `Rehydrate` recebe o identificador e o instante já gravados e não gera nada. As duas passam pela mesma validação.

- **Base:** "Separe criação e reidratação. A reidratação não deve reaplicar movimentações, transições ou emissão de eventos."

## 18. Carteira

Implementado em `internal/domain/wallet`. O enunciado trata do assunto na seção 6.2. O controle de concorrência, a tabela e a unicidade por jogador e moeda são dos grupos 9 a 11 do `tasks.md` e estão nas seções 2 e 3 deste documento, ainda pendentes.

### 18.1. Conteúdo e encapsulamento

A carteira guarda identificador, jogador, saldo, versão e os instantes de criação e de atualização. A moeda é a do saldo. O estado é privado: `State()` devolve uma cópia, e o saldo só muda por `Credit` e `Debit`.

- **Base:** "Deve carregar identidade, jogador, moeda, saldo, versão e instantes de criação e atualização." e "mantendo a alteração do saldo sob controle do agregado".

### 18.2. Abertura e reidratação

`Open` gera o identificador e começa na versão 1. Os instantes de criação e de atualização são marcados em UTC, com precisão de microssegundo, a mesma do PostgreSQL. Com saldo inicial positivo, devolve também o movimento de crédito, de zero até o saldo, na própria versão 1. Com saldo zero não há movimento. `Rehydrate` reproduz o estado gravado sem gerar nada e recusa identificador vazio, saldo negativo, versão menor que 1 e instante ausente.

- **Base:** "A versão inicial é `1`" e, na seção 9, "a versão da carteira nessa abertura é `1`. Saldo inicial zero não cria `OPENING`, ledger nem esses eventos financeiros."

### 18.3. Crédito e débito

As duas operações calculam o novo saldo antes de alterar qualquer coisa. Só quando tudo passa a carteira muda: saldo novo, versão mais um e instante de atualização. Uma operação recusada deixa a carteira exatamente como estava.

Cada operação aceita devolve um `Movement` com direção, valor, saldo anterior, saldo posterior e versão. São os dados com que a camada de aplicação monta o lançamento do ledger e o evento.

- **Base:** "Débitos precisam preservar saldo maior ou igual a zero.", "A moeda de cada movimentação deve coincidir com a da carteira." e "depois da criação, incremente-a apenas quando houver mudança de saldo."

### 18.4. Erros

| Erro | Quando |
| --- | --- |
| `ErrInsufficientFunds` | débito maior que o saldo |
| `ErrCurrencyMismatch` | moeda diferente da carteira |
| `ErrBalanceOverflow` | crédito além do limite de `int64` |
| `ErrInvalidAmount` | valor que não é maior que zero, ou saldo inicial negativo |
| `ErrInvalidWallet` | estado inválido na reidratação, ou jogador vazio |

A carteira não conhece códigos de falha nem tipos de operação. Ela devolve um único erro de saldo insuficiente; quem decide entre `INSUFFICIENT_FUNDS` e `REVERSAL_INSUFFICIENT_FUNDS` é a camada que sabe se o débito veio de uma aposta ou de uma reversão.

- **Por quê:** `wallet` e `wager` não se importam, para que cada agregado proteja seu próprio estado.
- **Base:** "Seu código de falha deve ser diferente daquele usado para uma aposta sem saldo." A divisão de responsabilidade entre os pacotes é interpretação deste projeto.
