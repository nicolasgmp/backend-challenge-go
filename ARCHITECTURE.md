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

Implementado em `internal/infra/postgres` e em `migrations/`. O enunciado trata do assunto nas seções 4 ("Acesso ao banco") e 5.

### 2.1. Biblioteca

`pgx` v5, com SQL escrito à mão. Não há ORM, gerador de código nem construtor de consultas: cada `SELECT`, `INSERT`, `UPDATE`, lock e constraint aparece por extenso no repositório ou na migration.

- **Base:** "`pgx` com SQL explícito é preferencial" e "Transações, locks e constraints devem permanecer explícitos e verificáveis."

### 2.2. Quem abre e quem usa a transação

A transação SQL é aberta pelo caso de uso, por meio do `TxRunner`, e não pelos repositórios.

1. O caso de uso chama `TxRunner.Run` com uma função.
2. `Run` abre a transação, define o prazo de espera por lock (`lock_timeout`) só para ela e guarda a transação no `context.Context`.
3. Cada repositório chamado dentro da função lê a transação do contexto e executa nela.
4. Se a função devolve `nil`, `Run` confirma. Se devolve erro, ou se há pânico, `Run` desfaz tudo.

| Regra | Como é garantida |
| --- | --- |
| Todas as escritas de uma operação no mesmo commit | todos os repositórios usam a transação do contexto |
| Escrita ou lock fora de transação | o repositório devolve `postgres.ErrNoTransaction` |
| `Run` chamado dentro de outro `Run` | participa da transação de fora por meio de um `SAVEPOINT`; se o bloco de dentro falha, só ele é desfeito e a transação de fora continua utilizável |
| Leitura consistente (`RunReadOnly`) | `REPEATABLE READ READ ONLY`: todas as leituras enxergam o mesmo instante e nenhuma escrita é aceita |

O caso do `Run` aninhado é o do consumidor SQS, que abre a transação para gravar a inbox e chama o caso de uso dentro dela.

- **Base:** "Documente em `ARCHITECTURE.md` a biblioteca escolhida, o mapeamento de `Money` e a delimitação da transação SQL entre os repositórios."

### 2.3. Erros do banco

Os repositórios nunca devolvem um erro do `pgx`. Cada erro é traduzido:

| Situação no PostgreSQL | Erro devolvido |
| --- | --- |
| violação de índice único (`23505`) | `app.ErrUniqueViolation`, com o nome da constraint na mensagem |
| falha de serialização (`40001`), deadlock (`40P01`), prazo de lock vencido (`55P03`) | `app.ErrTransient` |
| conexão perdida ou recusada, servidor encerrando, prazo ou cancelamento do contexto | `app.ErrTransient` |
| consulta sem linha | `app.ErrNotFound` |
| qualquer outro | erro comum, só com o código e o nome da constraint |

A mensagem nunca traz a linha recusada, a URL de conexão nem a senha. O construtor do pool devolve um erro fixo para URL inválida.

### 2.4. Tabelas e o que o banco impõe

Valores monetários são `BIGINT` em unidades mínimas mais `CHAR(3)` com a moeda (seção 1.7). Identificadores internos são `uuid`, gerados na aplicação.

| Tabela | Regras impostas pelo banco |
| --- | --- |
| `wallets` | saldo maior ou igual a zero; versão maior ou igual a 1; uma carteira por jogador e moeda |
| `wager_transactions` | tipo conhecido; estado nunca `PENDING`; valor e saldo resultante não negativos; origem externa exige provedor, id externo, chave, hash, rodada e jogo, e origem interna exige `OPENING` sem nenhum deles; código de falha presente só em `REJECTED` e `FAILED`; `PENDING_REFERENCE` exige prazo e próxima tentativa; reversão processada exige a referência resolvida |
| `wager_transactions` (índices únicos) | chave por provedor; id externo por provedor; um `OPENING` por carteira; uma reversão `PROCESSED` por referência |
| `wager_transactions` (trigger) | nenhuma alteração em linha `PROCESSED`, `REJECTED` ou `FAILED` |
| `wallet_ledger_entries` | valor maior que zero; saldos não negativos; saldo posterior igual ao anterior mais ou menos o valor, conforme a direção; um lançamento por transação e um por versão, em cada carteira |
| `wallet_ledger_entries` (triggers) | `UPDATE`, `DELETE` e `TRUNCATE` recusados |
| `inbox` | uma linha por consumidor e mensagem |
| `outbox` (trigger) | as colunas do evento (id, agregado, grupo, tipo, versão, payload, correlação, causa, ocorrência) não mudam; só as de controle de publicação |

Duas regras desta lista não estão no enunciado e foram acrescentadas porque o código depende delas: o prazo obrigatório em `PENDING_REFERENCE`, que o worker usa para encerrar a espera, e a referência obrigatória numa reversão processada, sem a qual a linha escaparia do índice que impede duas reversões.

- **Por quê no banco:** as regras continuam valendo se o lock da carteira não for tomado, se houver um defeito no código Go ou se alguém escrever direto no banco.
- **Base:** garantias 3, 5 e 8 da seção 5: "As invariantes financeiras devem ser garantidas no banco", "O ledger deve ser append-only" e "Unicidade, não negatividade e imutabilidade do ledger devem ser impostas pelo schema, pelas constraints e pelos mecanismos de proteção do banco."

### 2.5. Migrations

Cinco migrations em `migrations/`, uma por tabela, cada uma com `up` e `down`. O `down` remove tudo o que o `up` criou, inclusive funções e triggers. Um teste aplica todas, reverte uma a uma até não restar tabela, função, trigger nem índice, e aplica de novo.

Os testes de integração usam PostgreSQL real (`postgres:17.6-alpine`) num container. Cada teste recebe um banco próprio, copiado de um modelo com as migrations já aplicadas, para que um teste não veja os dados de outro.

- **Base:** "Migrations versionadas, com aplicação e reversão documentadas". Os comandos estão no `README.md` (grupo 18 do `tasks.md`).

## 3. Controle de concorrência e locks

_Pendente (grupo 11 do `tasks.md`)._

## 4. Idempotência

### 4.1. Hash do conteúdo

Implementado em `internal/app/payloadhash`. Cada operação externa guarda o SHA-256, em hexadecimal, de um JSON canônico dos seus campos de negócio.

| Regra | Como |
| --- | --- |
| Campos | `externalTransactionId`, `gameId`, `kind`, `money`, `playerId`, `providerId`, `referenceExternalTransactionId` (só quando existe), `roundId` e `walletId` |
| Ordem | alfabética em todos os níveis; dentro de `money`, `amount` e depois `currency` |
| Formato | sem espaços, UTF-8, saída do `encoding/json` da biblioteca padrão |
| Fora do cálculo | a chave de idempotência e os metadados de transporte: `messageId`, `occurredAt`, headers, `correlationId` |

O JSON é escrito a partir dos valores de domínio já validados, nunca reaproveitado da requisição. Por isso a ordem das chaves, os espaços e a forma como o cliente escreveu o corpo não alteram o hash, e a mesma operação recebida por HTTP e por SQS produz o mesmo hash.

- **Normalização:** `Money` só aceita uma escrita por valor (`25.00`, nunca `25`, `25.0` ou `025.00`), então não há o que normalizar. Os identificadores de texto são comparados exatamente como chegaram. Os UUID (`playerId` e `walletId`) são reescritos na forma canônica, em minúsculas e com hífens (seção 16.2), de modo que duas escritas do mesmo UUID dão o mesmo hash.
- **Para quem recalcular o hash em outra linguagem:** o `encoding/json` escreve `<`, `>` e `&` dentro de textos como `\u003c`, `\u003e` e `\u0026`.
- **Exemplo:** `{"externalTransactionId":"transaction-123","gameId":"fortune-chimp","kind":"BET","money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","providerId":"provider-a","roundId":"round-987","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`
- **Base:** "Persista um hash determinístico dos campos de negócio, usando JSON canônico com ordenação de chaves. Exclua a chave de idempotência e os metadados de transporte desse cálculo. Documente algoritmo, campos e normalizações, garantindo equivalência entre HTTP e SQS."

### 4.2. Demais regras

_Pendente (grupo 11 do `tasks.md`)._

## 5. Máquina de estados da transação

Implementado em `internal/domain/wager`. O enunciado trata do assunto na seção 6.3. A tabela, o `CHECK` que impede `PENDING` gravado e o trigger de estado terminal são do grupo 9 do `tasks.md` e ainda não existem.

### 5.1. Estados e transições

```
PENDING ──────────────> PROCESSED
   │ ──────────────────> REJECTED
   └──> PENDING_REFERENCE ──> PROCESSED
                          ──> REJECTED
                          ──> FAILED
```

| Método | Origem permitida | O que registra |
| --- | --- | --- |
| `MarkProcessed` | `PENDING`, `PENDING_REFERENCE` | saldo resultante, referência interna resolvida, instante de conclusão |
| `MarkRejected` | `PENDING`, `PENDING_REFERENCE` | código de rejeição, instante de conclusão |
| `MarkPendingReference` | `PENDING` | prazo da espera e próxima tentativa |
| `MarkFailed` | `PENDING_REFERENCE` | `PROCESSING_FAILED`, instante de conclusão |

Qualquer outra origem devolve `wager.ErrInvalidTransition` e deixa a transação como estava. `PROCESSED`, `REJECTED` e `FAILED` são terminais: nenhum método sai deles. Não existe método que volte a `PENDING`.

- **`PENDING` só existe em memória.** É o estado em que a transação nasce. Uma operação sem dependência é concluída no mesmo commit, então o banco nunca guarda `PENDING`.
- **`MarkRejected` só aceita código de rejeição.** Código vazio, desconhecido, de entrada inválida ou `PROCESSING_FAILED` são recusados.
- **Base:** "A transação inicia em `PENDING`. As transições para processamento, espera por referência, rejeição e falha permanente devem ser validadas pelo domínio.", "Uma transação terminal não deve sofrer novas transições." e "Operações sem dependências podem ser concluídas de forma síncrona, sem commit intermediário de aceite."

### 5.2. Criação e reidratação

- **`NewExternal`** cria a operação de um provedor em `PENDING`. Julga só o que dá para julgar olhando a requisição: tipo que não é externo (`UNSUPPORTED_KIND`), valor fora da política do tipo (`INVALID_AMOUNT_FOR_KIND`) e referência faltando ou sobrando (`MISSING_REFERENCE`, `UNEXPECTED_REFERENCE`). São entradas inválidas: nada é gravado. Carteira, jogador, dado do provedor ou `correlationId` vazio devolve `wager.ErrInvalidTransaction`, que indica defeito de quem chamou, não erro do cliente.
- **`NewOpening`** cria a transação interna de abertura: sem provedor, id externo, chave, hash, rodada, jogo ou referência, com valor maior que zero, já em `PROCESSED`.
- **`correlationId`:** as duas criações recebem o identificador de correlação da requisição e o guardam na transação. O worker que resolve uma pendência minutos depois usa esse valor nos eventos que emite, para que eles continuem ligados à requisição original.
- **`Rehydrate`** reproduz o estado gravado sem aplicar transição. Recusa estado incoerente: identificador ou instante ausente, valor negativo ou não inicializado, tipo ou estado desconhecido, operação externa sem os dados do provedor, `OPENING` com dados de provedor, `REJECTED` ou `FAILED` sem código de falha, código de falha em outro estado, e `PENDING_REFERENCE` sem prazo.

| Tipo | Valor | Referência |
| --- | --- | --- |
| `BET` | maior que zero | não aceita |
| `WIN` | maior que zero | opcional |
| `LOSS` | exatamente zero | não aceita |
| `REFUND` | maior que zero | obrigatória |
| `ROLLBACK` | maior que zero | obrigatória |

- **Base:** "`OPENING` é reservado à abertura interna de carteira. Rejeite esse tipo quando enviado por HTTP ou SQS.", "zero é aceito no saldo inicial e em `LOSS`; `BET`, `WIN`, `REFUND` e `ROLLBACK` exigem valor maior que zero." e "Separe criação e reidratação."

### 5.3. Falha transitória e falha permanente

`FAILED` só é alcançado a partir de `PENDING_REFERENCE`, quando a resolução de uma pendência esbarra em erro inesperado cinco vezes seguidas.

| Situação na tentativa de resolver | Método | Efeito |
| --- | --- | --- |
| referência ainda ausente, ou erro transitório | `Reschedule` | conta a tentativa, zera os erros inesperados, marca a próxima tentativa |
| erro inesperado | `RecordUnexpectedError` | conta a tentativa e o erro; devolve verdadeiro no quinto seguido |
| quinto erro inesperado seguido | `MarkFailed` | `FAILED` com `PROCESSING_FAILED` |

A classificação de um erro como transitório (conexão perdida, timeout, deadlock, `lock_timeout`) ou inesperado (todo o resto) é feita na camada de aplicação, no grupo 11; o domínio só conta.

Numa operação síncrona, uma falha de infraestrutura desfaz a transação SQL e nada é gravado: não existe `FAILED` para esse caso, e quem enviou pode reenviar.

- **Interpretação adotada:** o enunciado define `FAILED` mas não diz que fluxo o produz. Gravar `FAILED` para uma operação síncrona impediria o reenvio do mesmo `externalTransactionId`, então o estado ficou restrito à pendência que não consegue ser resolvida. O limite de cinco é escolha deste projeto.
- **Base:** "`FAILED`: Falha permanente de infraestrutura registrada para auditoria; estado terminal" e "Documente a máquina de estados e como distingue falhas transitórias de falhas permanentes."

### 5.4. Intervalo entre tentativas

`wager.NextAttempt` devolve o instante da próxima tentativa: 1 segundo depois da primeira, dobrando a cada tentativa (2, 4, 8, 16) até o teto de 30 segundos, e nunca depois do prazo da espera. Quando o intervalo passaria do prazo, a próxima tentativa é o próprio prazo; é a última.

- **Base:** "Um worker deve tentar novamente com backoff exponencial". Os valores de 1 e 30 segundos são escolha deste projeto. O prazo e o worker são dos grupos 11 e 16 do `tasks.md` e serão descritos na seção 7.

## 6. Reversões (`REFUND` e `ROLLBACK`)

As regras estão em `internal/domain/wager` (`EffectOf` e `ValidateReference`). O enunciado trata do assunto na seção 7. A busca da referência no banco, a checagem de saldo e o índice único são dos grupos 9 a 11 do `tasks.md`.

### 6.1. O que cada tipo movimenta

| Tipo | Referência | Movimento |
| --- | --- | --- |
| `BET` | nenhuma | débito |
| `LOSS` | nenhuma | nenhum |
| `WIN` | nenhuma ou `BET` | crédito |
| `REFUND` | `BET` | crédito |
| `ROLLBACK` | `BET` | crédito |
| `ROLLBACK` | `WIN` | débito |
| `ROLLBACK` | `REFUND` | débito |

Qualquer combinação fora da tabela é rejeitada com `REFERENCE_KIND_NOT_ALLOWED`: `REFUND` de um `WIN`, `ROLLBACK` de um `ROLLBACK` ou de um `LOSS`, e assim por diante.

- **Base:** a tabela da seção 7 do enunciado: "`REFUND`: Devolve integralmente o valor de uma `BET` processada" e "`ROLLBACK`: Movimento contrário ao original. Desfaz integralmente uma `BET`, `WIN` ou `REFUND` processada".

### 6.2. Validação da referência

`ValidateReference` confere, nesta ordem, e para na primeira falha:

| Ordem | Situação da referência | Resultado |
| --- | --- | --- |
| 1 | ainda em `PENDING_REFERENCE` | a operação continua esperando (`wager.ErrReferencePending`) |
| 2 | `REJECTED` ou `FAILED` | `REFERENCE_NOT_PROCESSED` |
| 3 | tipo fora da tabela de 6.1 | `REFERENCE_KIND_NOT_ALLOWED` |
| 4 | provedor, jogador, carteira, moeda ou rodada diferentes | `REFERENCE_MISMATCH` |
| 5 | valor diferente | `REFERENCE_MISMATCH` |

A referência inexistente é tratada antes, pela camada de aplicação, que grava a operação como `PENDING_REFERENCE` (seção 7).

- **`WIN` com referência não confere valor.** O ganho não tem relação com o valor apostado; a referência só amarra o ganho a uma aposta processada da mesma rodada.
- **Base:** "A operação e sua referência devem concordar em provedor, jogador, carteira, moeda e rodada. O valor da reversão precisa ser igual ao valor referenciado; reversões parciais não fazem parte do desafio."

### 6.3. Uma reversão processada por operação

Cada operação aceita no máximo uma reversão em `PROCESSED`, seja `REFUND` ou `ROLLBACK`. Um `BET` já reembolsado que recebe um `ROLLBACK` tem o `ROLLBACK` rejeitado com `REFERENCE_ALREADY_REVERSED`, e o mesmo vale para a ordem inversa e para dois `REFUND`. Reversões rejeitadas ou pendentes não ocupam a vaga.

A regra será imposta pelo banco, com um índice único parcial sobre a referência interna resolvida (grupo 9). Por isso `MarkProcessed` recusa concluir um `REFUND` ou `ROLLBACK` sem a referência resolvida: sem ela a linha escaparia do índice.

- **Interpretação adotada:** o enunciado exige no mínimo que não haja duas reversões bem-sucedidas do mesmo tipo. Aqui a regra é mais restritiva: uma de qualquer tipo. `REFUND` e `ROLLBACK` de um `BET` devolvem o mesmo débito, então aceitar os dois seria devolver em dobro.
- **Base:** "Garanta que uma referência não receba duas reversões bem-sucedidas do mesmo tipo. Documente como trata combinações de `REFUND` e `ROLLBACK` sobre a mesma aposta, preservando a coerência financeira e impedindo devolução duplicada do mesmo débito."

### 6.4. Limitação: aposta reembolsada não volta a ser reversível

Quando o `REFUND` de um `BET` é desfeito por um `ROLLBACK`, o `BET` continua com sua vaga de reversão ocupada pelo `REFUND`, que segue `PROCESSED`. Um novo `REFUND` desse `BET` é rejeitado com `REFERENCE_ALREADY_REVERSED`.

- **Por quê:** liberar a vaga exigiria alterar o `REFUND`, que é uma linha terminal. O provedor que precisar reembolsar de novo envia um crédito por outro caminho.

### 6.5. Reversão sem saldo

Um `ROLLBACK` de `WIN` ou de `REFUND` debita a carteira. Se o saldo não cobre, a transação é gravada como `REJECTED` com `REVERSAL_INSUFFICIENT_FUNDS`, código diferente do `INSUFFICIENT_FUNDS` de uma aposta. A escolha entre os dois códigos é feita na camada de aplicação (grupo 11), conforme o tipo da operação.

- **Base:** "Uma reversão que precisaria debitar mais que o saldo disponível deve ser rejeitada e auditável. Seu código de falha deve ser diferente daquele usado para uma aposta sem saldo."

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

## 19. Eventos de domínio

Implementado em `internal/domain/events`. O enunciado trata do assunto na seção 11. A gravação na outbox, a publicação e o roteamento são dos grupos 10, 11 e 16 do `tasks.md` e estão na seção 9, ainda pendente.

### 19.1. Envelope

Todo evento tem `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId` (omitido quando vazio), `occurredAt`, `version` e `data`.

| Campo | Origem |
| --- | --- |
| `eventId` | UUIDv7 gerado na construção |
| `eventType`, `version` | fixados pelo construtor do evento; quem chama não informa |
| `aggregateId` | a transação, nos três eventos de transação; a carteira, em `WalletBalanceChanged` |
| `correlationId` | informado por quem chama; obrigatório |
| `causationId` | informado por quem chama; opcional |
| `occurredAt` | instante da construção, em UTC, com precisão de microssegundo |

Os campos do evento são privados. `Header()` devolve uma cópia do envelope, `Data()` devolve o conteúdo, e não há método que altere um evento construído.

O envelope guarda também a carteira do evento, que não vai para o JSON: ela será a chave de agrupamento na fila de saída.

- **Base:** "O envelope deve conter `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId` opcional, `occurredAt`, `version` e `data` tipado." e "Tipo e versão devem ser definidos pelo construtor do evento."

### 19.2. Um tipo concreto por evento

| Evento | Conteúdo de `data` |
| --- | --- |
| `WagerTransactionProcessed` | dados da transação e `balance`, o saldo resultante |
| `WagerTransactionRejected` | dados da transação e `failureCode` |
| `WagerTransactionPendingReference` | dados da transação e `referenceExpiresAt` |
| `WalletBalanceChanged` | `walletId`, `transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter`, `walletVersion` |

Os dados da transação são `transactionId`, `walletId`, `playerId`, `kind`, `money` e, quando existem, `providerId`, `externalTransactionId`, `roundId`, `gameId` e `referenceExternalTransactionId`. Na abertura de carteira (`OPENING`) esses cinco últimos não existem e são omitidos do JSON.

A construção recusa, com `events.ErrInvalidEvent`: `correlationId` vazio, identificador obrigatório vazio, valor não inicializado, `failureCode` que não é de rejeição, pendência sem referência ou sem prazo, e mudança de saldo com direção desconhecida, valor que não é maior que zero ou versão menor que 1.

- **Por quê o pacote não importa `wager` nem `wallet`:** o evento é montado com valores simples do domínio, então mudar um agregado não muda o contrato publicado sem que alguém altere este pacote.
- **Base:** "Defina tipos concretos por evento." e "O payload de `WalletBalanceChanged` deve incluir `walletId`, `transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter` e `walletVersion`."

### 19.3. JSON

Instantes saem em RFC 3339, em UTC. Valores monetários saem como `{"amount":"25.00","currency":"BRL"}`. Os testes comparam o JSON de cada evento, byte a byte, com o esperado.

```json
{
  "eventId": "0192f2a0-0000-7000-8000-000000000001",
  "eventType": "WalletBalanceChanged",
  "aggregateId": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "correlationId": "correlation-1",
  "causationId": "message-1",
  "occurredAt": "2026-01-01T12:00:00Z",
  "version": 1,
  "data": {
    "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
    "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
    "direction": "DEBIT",
    "money": {"amount": "25.00", "currency": "BRL"},
    "balanceBefore": {"amount": "1000.00", "currency": "BRL"},
    "balanceAfter": {"amount": "975.00", "currency": "BRL"},
    "walletVersion": 2
  }
}
```

- **Base:** "Use timestamps UTC em RFC 3339 e valores monetários em strings decimais."

## 20. Teste de arquitetura

`internal/archtest` roda `go list` sobre o módulo e falha quando um pacote importa o que não deve. Os imports dos arquivos de teste também são conferidos. É um teste comum, executado por `go test ./...`.

| Regra | Motivo |
| --- | --- |
| cada pacote de `internal/domain` só importa os pacotes de domínio da tabela abaixo | manter o grafo sem ciclos e os agregados separados |
| `internal/domain` não importa `net/http` nem seus subpacotes, nem biblioteca externa além de `google/uuid` | regra do enunciado |
| `internal/domain` não importa as camadas de fora (`app`, `infra`) | idem |
| `internal/app` não importa `internal/infra` | a aplicação define as portas; a infraestrutura as implementa |
| `infra/httpapi` não importa `infra/postgres` nem `infra/sqs` | o HTTP fala só com os casos de uso |

| Pacote | Pode importar |
| --- | --- |
| `money`, `ids`, `failure` | nenhum pacote de domínio |
| `ledger` | `money`, `ids` |
| `wallet` | `money`, `ids`, `ledger` |
| `wager` | `money`, `ids`, `failure`, `ledger` |
| `events` | `money`, `ids`, `ledger`, `failure` |

Um segundo teste alimenta as regras com imports inventados, um permitido e um proibido de cada tipo, para provar que elas acusam o que devem.

- **Limitação:** o teste olha os imports diretos. Um pacote da biblioteca padrão que use `net/http` por dentro não é acusado.
- **Base:** "O domínio deve permanecer independente de Fx, HTTP, SQS e bibliotecas de persistência. A organização dos pacotes fica a critério do candidato." O grafo entre os pacotes é escolha deste projeto.

## 21. Camada de aplicação: portas

`internal/app` define as interfaces que a infraestrutura implementa. Os casos de uso só conhecem essas interfaces, e por isso não importam `pgx`, o SDK da AWS nem `net/http`.

| Porta | O que deve garantir |
| --- | --- |
| `TxRunner` | `Run` executa a função numa transação SQL: confirma se ela devolve `nil`, desfaz se devolve erro. Chamado dentro de outra transação, participa dela. `RunReadOnly` dá uma visão única dos dados e não permite escrita |
| `WalletRepository` | `GetForUpdate` trava a linha da carteira até o fim da transação e falha fora de uma. `Insert` devolve `ErrUniqueViolation` para jogador e moeda repetidos |
| `TransactionRepository` | as buscas por chave e por identificador externo sempre filtram por provedor. `Insert` e `Update` devolvem `ErrUniqueViolation` quando o banco recusa uma chave, uma operação ou uma segunda reversão processada |
| `LedgerRepository` | só insere e lê; não há alteração nem exclusão. `Totals` soma em inteiros |
| `OutboxStore` | `Insert` grava na transação em curso. `Claim` reserva registros por um prazo, sem que dois publishers peguem o mesmo |
| `InboxStore` | `Register` diz se a mensagem é nova, repetida ou repetida com outro conteúdo; `Complete` marca a conclusão. As duas rodam na transação do tratamento |
| `Clock` | o instante atual, para que os testes controlem o tempo |
| `Metrics` | contadores e medidas, com rótulos de conjuntos fechados |

Toda busca que não encontra devolve `app.ErrNotFound`. Falhas passageiras da infraestrutura chegam como `app.ErrTransient`.

`internal/app/apptest` tem implementações em memória dessas portas, usadas só nos testes unitários dos casos de uso. Elas imitam as regras de unicidade do banco e desfazem as escritas quando a transação falha, inclusive o bloco de dentro de um `Run` aninhado. Não imitam tudo: chaves estrangeiras, o prazo de reserva da outbox e a recusa de escrita em `RunReadOnly` só existem no banco real. Os testes de integração usam PostgreSQL real.

- **Base:** "O domínio deve permanecer independente de Fx, HTTP, SQS e bibliotecas de persistência." e, na seção 13, "Não substitua toda a infraestrutura por mocks."
