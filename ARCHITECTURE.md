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

O valor é gravado como `BIGINT` de unidades mínimas e a moeda como `CHAR(3)`, sem conversão entre o tipo do domínio e o do banco. As tabelas estão na seção 2.4.

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
| Leitura consistente (`RunReadOnly`) | `REPEATABLE READ READ ONLY`: todas as leituras enxergam o mesmo instante e nenhuma escrita é aceita. Só vale quando é a transação de fora; chamado dentro de um `Run`, herda a transação que já existe |

O caso do `Run` aninhado é o do consumidor SQS, que abre a transação para gravar a inbox e chama o caso de uso dentro dela.

- **Base:** "Documente em `ARCHITECTURE.md` a biblioteca escolhida, o mapeamento de `Money` e a delimitação da transação SQL entre os repositórios."

### 2.3. Erros do banco

Os erros devolvidos pelo servidor são traduzidos antes de sair do repositório:

| Situação no PostgreSQL | Erro devolvido |
| --- | --- |
| violação de índice único (`23505`) | `app.ErrUniqueViolation`, com o nome da constraint na mensagem |
| falha de serialização (`40001`), deadlock (`40P01`), prazo de lock vencido (`55P03`) | `app.ErrTransient` |
| conexão perdida ou recusada, servidor encerrando, prazo ou cancelamento do contexto | `app.ErrTransient` |
| consulta sem linha | `app.ErrNotFound` |
| qualquer outro | erro comum, só com o código e o nome da constraint |

A mensagem de um erro do servidor traz só o código e o nome da constraint: nunca a linha recusada, a URL de conexão nem a senha. O construtor do pool devolve um erro fixo para URL inválida. Um erro que não vem do servidor nem é de conexão (por exemplo, um parâmetro que o `pgx` não consegue codificar) segue com o texto original, porque indica defeito de programação e o texto ajuda a encontrá-lo.

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

O que o banco **não** confere, e fica a cargo dos agregados e dos casos de uso:

| Não conferido pelo banco | Quem garante |
| --- | --- |
| coerência entre linhas: o lançamento ser da mesma carteira, moeda e valor da transação; o saldo da carteira só mudar junto com um lançamento | o caso de uso, que grava tudo na mesma transação a partir do mesmo `Movement` |
| moeda dentro da lista aceita, texto não vazio nos dados do provedor | `Money` e os identificadores do domínio |
| exclusão de transação sem lançamento e de registro da outbox | a aplicação não tem nenhum `DELETE` |
| o dono das tabelas desativar um trigger | fora do escopo: exigiria um usuário de aplicação separado do dono |

- **Por quê no banco:** as regras continuam valendo se o lock da carteira não for tomado, se houver um defeito no código Go ou se alguém escrever direto no banco.
- **Base:** garantias 3, 5 e 8 da seção 5: "As invariantes financeiras devem ser garantidas no banco", "O ledger deve ser append-only" e "Unicidade, não negatividade e imutabilidade do ledger devem ser impostas pelo schema, pelas constraints e pelos mecanismos de proteção do banco."

### 2.5. Migrations

Cinco migrations em `migrations/`, uma por tabela, cada uma com `up` e `down`. O `down` remove tudo o que o `up` criou, inclusive funções e triggers. Um teste aplica todas, reverte uma a uma até não restar tabela, função, trigger nem índice, e aplica de novo.

Os testes de integração usam PostgreSQL real (`postgres:17.6-alpine`) num container, e executam os mesmos arquivos `.sql` de `migrations/`. Os alvos `make migrate-up` e `make migrate-down` usam a ferramenta `golang-migrate` sobre os mesmos arquivos. Cada teste recebe um banco próprio, copiado de um modelo com as migrations já aplicadas, para que um teste não veja os dados de outro.

- **Base:** "Migrations versionadas, com aplicação e reversão documentadas". Os comandos estão no `README.md` (grupo 18 do `tasks.md`).

### 2.6. Repositórios

| Decisão | Motivo |
| --- | --- |
| identificadores vão e voltam do `pgx` como texto | o banco valida o `uuid`; o domínio não precisa expor bytes nem conhecer tipos do `pgx` |
| instantes voltam convertidos para UTC | o `pgx` devolve no fuso da máquina; o domínio trabalha em UTC |
| campo ausente no domínio vira `NULL` | as constraints do tipo "presente só neste estado" dependem de `NULL` de verdade |
| `Update` da transação grava só as colunas que mudam depois da criação | estado, referência resolvida, código de falha, saldo resultante, próxima tentativa, contadores e instantes |
| o payload da outbox é `jsonb` | o banco guarda o conteúdo, não os bytes: o texto publicado pode ter as chaves em outra ordem, com o mesmo conteúdo |
| a reserva da outbox (`Claim`) é um único comando com `FOR UPDATE SKIP LOCKED` | um publisher não espera pelo outro e dois nunca pegam o mesmo registro com a reserva válida |

Limitações conhecidas:

- **Relógios:** a reserva da outbox usa o relógio do banco, e a próxima tentativa de um evento é marcada com o relógio da instância. Uma diferença entre os dois só atrasa ou adianta a publicação nessa mesma diferença; nenhum evento é perdido.
- **Publisher travado além da reserva:** se um publisher fica parado por mais que o prazo da reserva e depois registra uma falha, ele libera a reserva que outro publisher já tinha tomado. O efeito é uma publicação repetida com o mesmo `eventId`, que o contrato de entrega já admite.
- **Soma do ledger:** `Totals` soma em inteiros. Se a soma de créditos de uma carteira passar de 92 quatrilhões em unidades inteiras, a consulta falha com erro; não há arredondamento nem valor errado.

## 3. Controle de concorrência e locks

Implementado em `internal/app` (casos de uso) sobre `internal/infra/postgres`. O enunciado trata do assunto na seção 8.

### 3.1. Estratégia: lock pessimista por carteira

Toda operação que lê ou altera um saldo começa travando a linha da carteira com `SELECT ... FOR UPDATE`. É a primeira instrução da transação, antes de qualquer outra leitura ou escrita.

```
BEGIN
  define o prazo de espera por lock (só para esta transação)
  SELECT ... FROM wallets WHERE id = $1 FOR UPDATE          <- primeiro
  busca por chave e por operação (replay ou conflito)
  resolve a referência, se houver
  aplica no agregado (débito, crédito ou nada)
  INSERT wager_transactions
  UPDATE wallets (saldo, versão)                             <- só se houve movimentação
  INSERT wallet_ledger_entries                               <- só se houve movimentação
  INSERT outbox
COMMIT
```

| Pergunta | Resposta |
| --- | --- |
| Duas operações da mesma carteira | a segunda espera a primeira confirmar ou desfazer, e só então lê o saldo |
| Carteiras diferentes | não esperam uma pela outra; cada lock é de uma linha |
| Lock global ou em memória | não existe; a coordenação é toda do banco, então vale entre processos e instâncias |
| Espera sem fim | não: o prazo é de 5 segundos (configurável). Vencido, a operação falha como indisponibilidade transitória, sem gravar nada |
| O mesmo caminho para HTTP, SQS e worker de referências | sim: HTTP e SQS chamam o mesmo caso de uso, e o worker reavalia a pendência com as mesmas regras e a mesma ordem de lock |

- **Por quê pessimista:** a mesma trava que protege o saldo também põe em fila a checagem de idempotência e a de reversão anterior. Com controle otimista essas duas checagens precisariam de retry próprio.
- **Alternativas descartadas:** controle otimista por versão com retry; atualização condicionada (`UPDATE ... WHERE balance >= $1`) sem lock, que não põe em fila a checagem de idempotência.
- **Base:** "A coordenação deve ocorrer por carteira. Escolha locking pessimista, controle otimista com retry limitado, atualização atômica condicionada ou uma combinação justificável." e "locks globais são proibidos".

### 3.2. O banco como segunda barreira

O lock evita a disputa; as constraints garantem o resultado mesmo que o lock falhe ou não seja tomado (seção 2.4): saldo não negativo, uma operação por chave e por identificador externo, um lançamento por transação e por versão, uma reversão processada por referência.

### 3.3. Disputa que o lock não cobre

Duas requisições com a mesma chave de idempotência mas com `walletId` diferentes travam carteiras diferentes e chegam juntas ao `INSERT`. O índice único deixa passar uma só. A que perde recebe violação de unicidade, e o tratamento é:

1. desfazer a transação;
2. executar o caso de uso de novo, uma única vez;
3. na segunda passada, a busca prévia encontra a linha vencedora e responde conforme ela: replay ou conflito.

Uma segunda violação seguida é devolvida como erro.

### 3.4. Demonstração

Os testes de `test/integration` rodam contra PostgreSQL real:

| Cenário | Resultado conferido |
| --- | --- |
| duas apostas distintas de 80,00 sobre 100,00, ao mesmo tempo | uma `PROCESSED`, uma `REJECTED` com `INSUFFICIENT_FUNDS`, saldo 20,00, um único débito; o reenvio das duas não muda nada |
| a mesma aposta enviada 50 vezes em paralelo | um débito, uma transação, 49 respostas de replay com o mesmo `transactionId` |
| 20 créditos de 10,00 em paralelo sobre saldo zero | saldo 200,00, versão 21, 20 lançamentos |
| uma carteira travada | a aposta em outra carteira conclui; a aposta na carteira travada só conclui depois da liberação |
| `REFUND` e `ROLLBACK` da mesma aposta, ao mesmo tempo | exatamente uma reversão `PROCESSED`; a outra `REJECTED` com `REFERENCE_ALREADY_REVERSED` |
| mesma chave em duas carteiras, ao mesmo tempo | uma processada e um conflito `IDEMPOTENCY_KEY_REUSED` |
| falha injetada em cada escrita | nenhuma transação, lançamento, evento ou mudança de saldo confirmados |

Os cenários de saldo terminam reconciliando a carteira: o saldo gravado é igual a créditos menos débitos do ledger. Outro teste reconcilia 200 vezes uma carteira que recebe créditos em paralelo e não aceita nenhuma divergência falsa. A repetição com três instâncias do serviço é do grupo 19 do `tasks.md`.

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

### 4.2. Duas identidades, as duas por provedor

| Identidade | Tupla única no banco | Para que serve |
| --- | --- | --- |
| chave de idempotência | `(provider_id, idempotency_key)` | reconhecer o reenvio da mesma requisição |
| operação financeira | `(provider_id, external_transaction_id)` | impedir que a mesma operação seja aplicada com outra chave |

A chave é gravada exatamente como chegou; o servidor nunca a troca por uma calculada. Como as duas tuplas incluem o provedor, a chave e o identificador de um provedor não encontram nem afetam os de outro.

A chave, o hash e o resultado ficam na própria linha da transação (`wager_transactions`). Não há tabela separada nem memória local, então a idempotência sobrevive ao reinício de todos os processos e vale em qualquer instância.

- **Base:** "Idempotência deve ser persistente e sobreviver ao reinício de todos os processos." e "o servidor não deve substituir silenciosamente uma chave recebida por outra calculada."

### 4.3. Decisão a cada requisição

Com a carteira já travada, o caso de uso busca pela chave e pelo identificador externo e decide:

| A chave existe | A operação existe | Hash | Resultado |
| --- | --- | --- | --- |
| não | não | | processa |
| sim | | igual | replay: devolve o resultado gravado, com `idempotentReplay: true` |
| sim | | diferente | conflito `IDEMPOTENCY_KEY_REUSED` |
| não | sim | | conflito `TRANSACTION_ALREADY_REGISTERED` |

- **Replay não reaplica nada:** não há movimentação, lançamento nem evento novos.
- **Saldo do replay:** é o `result_balance` gravado no processamento original, mesmo que a carteira já tenha recebido outras movimentações.
- **Replay de rejeição e de pendência:** devolve a mesma rejeição, com o mesmo `failureCode`, ou o mesmo `PENDING_REFERENCE`; nunca tenta de novo.
- **Interpretação adotada:** o enunciado só proíbe reaplicar uma operação recebida com outra chave. Aqui isso é um conflito explícito, e não um replay, porque uma chave nova para uma operação antiga indica defeito no cliente.
- **Base:** as quatro regras da seção 9, "Chave e conteúdo equivalentes [...]", "Chave reutilizada com conteúdo diferente [...]", "não pode ser reaplicada usando outra chave" e "o replay deve devolver o saldo observado no processamento original".

### 4.4. Entrada inválida não ocupa a chave

Uma requisição recusada como entrada inválida (seção 13.2) não grava nada. O mesmo `externalTransactionId` e a mesma chave podem ser reenviados depois de corrigidos. Uma rejeição de negócio, ao contrário, grava a transação como `REJECTED` e encerra aquele identificador.

- **Limitação:** uma operação rejeitada por jogador ou moeda divergentes também encerra o identificador. O provedor reenvia com outro `externalTransactionId`.

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
| referência ainda ausente | `Reschedule` | conta a tentativa, zera os erros inesperados, marca a próxima tentativa |
| erro transitório | nenhum | nada é gravado; a pendência continua vencida e é tentada de novo no próximo ciclo do worker |
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
- **Limitação:** um `WIN` que referencia uma aposta já reembolsada ou desfeita é aceito, e uma aposta com ganho pago ainda pode ser reembolsada. O enunciado só pede que o ganho aponte para uma aposta da mesma rodada; o `WIN` não ocupa nem consulta a vaga de reversão.
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

As regras estão em `internal/app` (`SubmitTransaction` e `ResolvePendingReference`). O laço do worker que as chama periodicamente é do grupo 16 do `tasks.md`.

### 7.1. Registro da espera

Um `REFUND`, um `ROLLBACK` ou um `WIN` com referência cuja operação referenciada ainda não chegou é gravado como `PENDING_REFERENCE`. Nesse momento:

- nada é movimentado e nenhum lançamento é criado;
- o prazo da espera é gravado: 5 minutos a partir do registro, configurável;
- a primeira tentativa é marcada para 1 segundo depois;
- o evento `WagerTransactionPendingReference` vai para a outbox, no mesmo commit.

A referência é sempre procurada por `(providerId, referenceExternalTransactionId)`, com o provedor da própria operação. Uma operação de outro provedor com o mesmo identificador não é encontrada.

### 7.2. Tentativas

Cada tentativa trava a carteira, relê a pendência com lock, confere que ela ainda está pendente e vencida, e reavalia a operação com as mesmas validações de uma operação recém-chegada.

| O que a tentativa encontra | Resultado |
| --- | --- |
| referência `PROCESSED` | a operação é aplicada: `PROCESSED`, com movimentação, lançamento e eventos; ou `REJECTED`, se alguma validação falhar |
| referência `REJECTED` ou `FAILED` | `REJECTED` com `REFERENCE_NOT_PROCESSED`, sem esperar o prazo |
| referência ausente ou também pendente, antes do prazo | reagenda: 2, 4, 8, 16 e depois 30 segundos, nunca além do prazo |
| referência ausente ou também pendente, no prazo ou depois dele | `REJECTED` com `REFERENCE_NOT_FOUND` e evento de rejeição |
| erro transitório | nada muda; a pendência continua vencida e é tentada no próximo ciclo |
| erro inesperado | conta um erro; no quinto seguido, `FAILED` com `PROCESSING_FAILED` (seção 5.3) |

- **Última tentativa:** a busca pela referência é feita antes de olhar o prazo. Se o serviço ficou parado além do prazo e a referência chegou nesse meio-tempo, a pendência é resolvida em vez de expirar.
- **Várias instâncias:** a pendência, o prazo e a próxima tentativa estão no banco. Duas instâncias que peguem a mesma pendência são postas em fila pelo lock da carteira; a segunda encontra o estado já mudado e não faz nada.
- **Duas reversões pendentes para a mesma referência:** quando a referência chega, a primeira a ser avaliada é processada e a outra é rejeitada com `REFERENCE_ALREADY_REVERSED`.
- **Correlação:** os eventos da resolução levam o `correlationId` da requisição original, guardado na transação.
- **TTL em vez de número de tentativas:** com um máximo de tentativas, o tempo total de espera dependeria do backoff. Com prazo, o provedor sabe até quando esperar.
- **Base:** "Persista a operação como `PENDING_REFERENCE` quando a referência ainda não tiver chegado. Um worker deve tentar novamente com backoff exponencial, inclusive após reinicialização da aplicação." e "Defina um número máximo de tentativas ou TTL. Quando esgotado, finalize como `REJECTED`, informando um código de referência não encontrada e produzindo o evento de rejeição. Explique também o comportamento quando a referência existe, mas ainda está pendente ou terminou sem sucesso."

### 7.3. Worker

O worker de referências é um laço em `internal/worker`: a cada intervalo chama `ResolveDuePendingReferences`, que lista as pendências vencidas e tenta cada uma na sua própria transação. Uma pendência que falha não impede as outras do mesmo ciclo.

Os dois workers (este e o publicador da outbox) usam o mesmo laço:

| Comportamento | Como |
| --- | --- |
| prazo por ciclo | cada ciclo roda com um contexto de prazo próprio |
| lote cheio | o ciclo seguinte começa na hora, sem esperar o intervalo |
| ciclo com erro | o erro é registrado em log e o laço continua |
| encerramento | o cancelamento do contexto interrompe a espera e o ciclo em andamento; `Run` retorna e registra em log que parou |

Um teste com PostgreSQL real roda o worker com prazo de espera de 2 segundos sobre um `REFUND` cuja referência nunca chega: a transação termina `REJECTED` com `REFERENCE_NOT_FOUND` e o evento de rejeição aparece na outbox. O worker desse teste usa uma instância do serviço diferente da que registrou a pendência, como aconteceria depois de um reinício.

## 8. Inbox e consumidor SQS

Implementado em `internal/infra/sqs` (cliente, filas, consumidor) e em `internal/infra/postgres` (inbox). O enunciado trata do assunto nas seções 6.5 e 10.

### 8.1. Filas

| Fila | Papel | Configuração |
| --- | --- | --- |
| `wager-transactions.fifo` | entrada de operações | visibility timeout de 30 s; redrive para a DLQ depois de 5 recebimentos |
| `wager-transactions-dlq.fifo` | mensagens que não puderam ser processadas | |
| `wallet-events.fifo` | saída de eventos (seção 9) | mesma configuração da de entrada |
| `wallet-events-dlq.fifo` | DLQ dos eventos | |

As quatro são criadas por `sqs.EnsureQueues`, que pode ser executada quantas vezes for preciso: criar uma fila que já existe com os mesmos atributos não muda nada. Um teste a executa duas vezes contra o MiniStack e confere os atributos. No Compose ela roda num job antes do serviço (grupo 18 do `tasks.md`).

- **Mudança em relação ao plano:** o plano previa um script de shell em `deploy/ministack/`. O provisionamento ficou em Go porque assim é testado com o mesmo cliente e contra o mesmo broker dos demais testes, sem depender de uma ferramenta de linha de comando dentro de um container.
- **Base:** "Provisione as filas `wager-transactions.fifo` e `wager-transactions-dlq.fifo`, incluindo a configuração de redrive."

### 8.2. Mensagem de entrada

O corpo é um envelope com `messageId`, `type` igual a `WagerTransactionRequested`, `occurredAt` e `data`. Em `data` vão os mesmos campos da requisição HTTP mais `idempotencyKey`. A leitura recusa campo desconhecido, no envelope e em `data`.

Os campos de `data` passam pela mesma conversão do HTTP (`app.RawOperation`) e chegam ao mesmo caso de uso. A mesma operação enviada pelos dois canais gera o mesmo hash de conteúdo, e a segunda é reconhecida como replay.

| Dado | De onde vem na fila |
| --- | --- |
| chave de idempotência | `data.idempotencyKey` |
| `correlationId` | atributo `correlationId` da mensagem; se não houver, o `messageId` |
| `causationId` dos eventos | o `messageId` |

Contrato do produtor: `MessageGroupId` igual ao `walletId` e `MessageDeduplicationId` igual ao `messageId`. A correção não depende disso: se o produtor usar outro grupo, duas mensagens da mesma carteira podem ser consumidas ao mesmo tempo e o lock da carteira as põe em fila; se repetir a mensagem depois da janela de deduplicação do FIFO, a inbox e a idempotência a reconhecem.

- **Base:** "HTTP e SQS devem compartilhar o caso de uso e as garantias de idempotência financeira. Na entrada por SQS, a chave é `data.idempotencyKey`, com deduplicação adicional pela inbox." e "Documente `MessageGroupId` e `MessageDeduplicationId`".

### 8.3. Tratamento de uma mensagem

```
recebe -> lê o envelope
  inválido ----------------------------------> DLQ com o motivo, apaga da origem
BEGIN
  INSERT inbox (consumidor, messageId, hash do corpo) ... ON CONFLICT DO NOTHING
    já registrada, mesmo hash --------------> COMMIT, apaga (duplicata)
    já registrada, outro hash --------------> ROLLBACK, DLQ, apaga
  caso de uso (num SAVEPOINT da mesma transação)
    entrada inválida ou conflito -----------> ROLLBACK, DLQ, apaga
    PROCESSED | REJECTED | PENDING_REFERENCE
  UPDATE inbox SET completed_at
COMMIT
apaga a mensagem
erro transitório ou inesperado -------------> ROLLBACK, adia a visibilidade
```

| Regra | Como é cumprida |
| --- | --- |
| inbox, domínio, ledger e eventos no mesmo commit | o registro da inbox, o caso de uso e a conclusão rodam na mesma transação SQL; se qualquer parte falha, nada é confirmado |
| a mensagem só sai da fila depois do commit | o `DeleteMessage` é a última chamada, depois de `Tx.Run` devolver sucesso |
| reentrega de mensagem já tratada | a inbox tem a linha com o mesmo hash: nada é reexecutado, a duplicata é contada em métrica e a mensagem é apagada |
| mesmo `messageId` com outro corpo | o hash difere: erro permanente, vai para a DLQ sem executar |
| rejeição de negócio | a transação `REJECTED` é gravada e a mensagem é apagada; não vai para a DLQ |
| referência pendente | a transação `PENDING_REFERENCE` e a conclusão da inbox são confirmadas juntas e a mensagem é apagada; o worker de referências assume |

- **Hash da inbox:** SHA-256 do corpo bruto da mensagem. É diferente do hash de conteúdo da seção 4.1, que cobre só os campos de negócio.
- **Identidade:** `(consumer_name, message_id)`, única no banco; o consumidor se chama `wager-transactions-consumer`.
- **Base:** "Use o `messageId` do envelope como identidade durável da mensagem para o consumidor e verifique seu hash em reentregas.", "Remova a mensagem da fila somente após o commit do seu tratamento durável." e "Rejeições de negócio confirmadas são terminais e permitem a remoção da mensagem."

### 8.4. Mensagens inválidas

Uma mensagem que nunca vai virar operação é enviada pelo próprio consumidor à DLQ, com o motivo no atributo `reason`, e apagada da origem no primeiro recebimento. Não se espera o redrive.

| Motivo (`reason`) | Situação |
| --- | --- |
| `INVALID_MESSAGE` | corpo que não é JSON, envelope incompleto ou com campo desconhecido |
| `UNKNOWN_MESSAGE_TYPE` | `type` diferente de `WagerTransactionRequested` |
| `MESSAGE_ID_REUSED` | `messageId` já registrado com outro corpo |
| um código da seção 13.2 | entrada inválida de domínio: `UNSUPPORTED_KIND` (inclui `OPENING`), `INVALID_AMOUNT_FOR_KIND`, `WALLET_NOT_FOUND` e os demais |
| `IDEMPOTENCY_KEY_REUSED`, `TRANSACTION_ALREADY_REGISTERED` | conflito de idempotência |
| `RETRIES_EXHAUSTED` | só na métrica: a mensagem chegou ao quinto recebimento com falha e vai para a DLQ pelo redrive |

- **Por quê não esperar o redrive:** numa fila FIFO a mensagem inválida seguraria todas as seguintes da mesma carteira por cinco recebimentos. Com o envio direto, a mensagem válida que vem atrás é processada em seguida.
- **Limitação:** o envio à DLQ e a remoção da origem são duas chamadas. Se o processo morrer entre elas, a mensagem é reentregue e vai à DLQ de novo; a deduplicação do FIFO descarta a cópia dentro de cinco minutos.
- **Base:** "erros permanentes ou tentativas esgotadas devem chegar à DLQ" e "Documente limites de tentativas, visibility timeout e tratamento de mensagens inválidas."

### 8.5. Falhas transitórias

Quando o tratamento falha por erro transitório ou inesperado, a mensagem não é apagada. O consumidor muda a visibilidade dela para adiar a reentrega, com atraso que dobra a cada recebimento (o atraso inicial é configurável, e o teto é de 60 segundos). No quinto recebimento com falha a visibilidade é zerada, e a entrega seguinte é o redrive do broker para a DLQ.

| Parâmetro | Valor |
| --- | --- |
| visibility timeout da fila | 30 s |
| recebimentos até a DLQ | 5 |
| long polling | 20 s por padrão |
| mensagens por lote | até 10, tratadas em paralelo |

- **Base:** "Falhas transitórias exigem retry com backoff".

### 8.6. Encerramento

Ao receber o cancelamento, o consumidor para de buscar mensagens. As que já estão em tratamento continuam com um contexto próprio, limitado pelo prazo de tratamento, e são concluídas e apagadas. Uma mensagem cujo tratamento falha durante o encerramento tem a visibilidade zerada, para que outra instância a pegue na hora. `Run` só retorna depois que o lote em andamento termina, e registra em log que parou.

- **Base:** "Em `SIGTERM`, pare de buscar trabalho e conclua o processamento em andamento dentro do prazo, ou libere sua visibilidade para reentrega segura."

### 8.7. Demonstração

Testes com PostgreSQL e MiniStack reais, em `test/integration`:

| Cenário | Resultado conferido |
| --- | --- |
| o consumidor confirma a transação e morre antes de apagar a mensagem | a reentrega é reconhecida pela inbox, a mensagem é apagada, e saldo, ledger e outbox não mudam |
| mesmo `messageId` com outro corpo; `OPENING`; e uma mensagem válida da mesma carteira logo atrás | as duas primeiras vão à DLQ com o motivo; a válida é processada sem esperar |
| banco inacessível e depois acessível | a mensagem fica na fila, o retry é contado, e ela é processada uma única vez depois da volta |
| banco inacessível o tempo todo | a mensagem chega à DLQ depois de 5 recebimentos, sem nenhum efeito financeiro |

## 9. Outbox e publicação de eventos

Implementado em `internal/worker` (ciclo de publicação), `internal/infra/postgres` (tabela `outbox`) e `internal/infra/sqs` (envio). O contrato dos eventos está na seção 19. O enunciado trata do assunto na seção 11.

### 9.1. Do commit à publicação

1. O caso de uso grava os eventos na tabela `outbox` dentro da mesma transação SQL da mudança (seção 3.1). Se a transação é desfeita, os eventos não existem.
2. Um worker separado, o publicador, consulta a outbox, reserva um lote, envia cada evento ao SQS e marca o que foi enviado.
3. Nenhum caso de uso fala com o SQS. Um evento só pode ser publicado depois do commit, porque antes disso o publicador não o enxerga.

- **Base:** "Eventos externos só podem ser publicados depois da confirmação da transação que os originou." e "Um worker separado publica os registros pendentes da outbox."

### 9.2. Vários publicadores

A reserva é um único comando SQL: escolhe os registros pendentes e vencidos com `FOR UPDATE SKIP LOCKED` e grava neles `locked_until = agora + prazo`. O prazo da reserva é de 30 segundos por padrão.

| Situação | Comportamento |
| --- | --- |
| dois publicadores ao mesmo tempo | cada um pula as linhas que o outro travou; nenhum espera e nenhum registro é reservado pelos dois |
| publicação com sucesso | grava `published_at` |
| publicação com falha | grava a tentativa e o erro, e reagenda: 1 s, 2 s, 4 s, até o teto de 60 s |
| publicador morre depois do commit da operação e antes de publicar | o evento continua pendente e outro publicador o envia |
| publicador morre depois de publicar e antes de marcar | a reserva vence e outro publicador republica, com o mesmo `eventId` |
| SQS fora do ar | os eventos ficam pendentes, com as tentativas contadas, e saem quando o SQS volta; as operações continuam sendo processadas |

- **Sem descarte:** não há limite de tentativas. Um evento cujo registro foi confirmado no banco nunca é abandonado.
- **Fora de transação:** o envio ao SQS acontece sem transação SQL aberta, para que um SQS lento não segure conexões do banco.
- **Base:** "Ele deve suportar múltiplos publishers, disputa por registros, backoff e recuperação de trabalho abandonado." e "Demonstre recuperação após interrupção entre commit e publicação e entre publicação e confirmação na outbox. Eventos pendentes devem ser assumidos por outra instância; republicações devem preservar o `eventId`."

### 9.3. Roteamento e consumo

| Item | Valor |
| --- | --- |
| destino | `wallet-events.fifo`, com `wallet-events-dlq.fifo` |
| `MessageGroupId` | o identificador da carteira do evento |
| `MessageDeduplicationId` | o `eventId` |
| corpo | o JSON do evento (seção 19.3) |

Contrato para quem consome:

- **A entrega é at-least-once.** O mesmo evento pode chegar mais de uma vez, sempre com o mesmo `eventId` e o mesmo conteúdo. O consumidor deve descartar repetições pelo `eventId`. A deduplicação do FIFO ajuda, mas só vale por cinco minutos.
- **A ordem entre eventos da mesma carteira não é garantida** quando há mais de um publicador. Quem precisa de ordem usa o `walletVersion` de `WalletBalanceChanged`.
- **O payload é um snapshot.** O banco impede a alteração das colunas do evento depois de gravado (seção 2.4).
- **Base:** "Provisione o destino dos eventos de saída e documente seus contratos de roteamento e consumo."

### 9.4. Atraso da outbox

A cada ciclo o publicador mede a idade do evento pendente mais antigo e a publica na métrica `outbox_lag_seconds`; sem pendentes, vale zero.

### 9.5. Demonstração

Testes com PostgreSQL e MiniStack reais, em `test/integration`:

| Cenário | Resultado conferido |
| --- | --- |
| dois publicadores sobre 100 eventos pendentes | os 100 chegam à fila e cada um foi enviado uma única vez |
| o publicador envia e morre antes de marcar | durante a reserva ninguém pega os eventos; vencida a reserva, outro publicador republica com o mesmo `eventId` e o mesmo corpo |
| SQS inacessível e depois acessível | as tentativas ficam registradas, uma operação nova é processada no meio, e os quatro eventos saem depois da volta |

## 10. Autenticação e autorização

Implementado em `internal/infra/auth` (validação do token), `internal/app` (política por chamador) e `deploy/keycloak` (realm). O enunciado trata do assunto na seção 2. A aplicação das regras em cada rota é do grupo 14 do `tasks.md`.

### 10.1. IdP e fluxo

O IdP é o Keycloak, na versão `26.7.5`, com o fluxo `client_credentials`: cada provedor e o serviço interno são clientes confidenciais, com `client_id` e segredo. O serviço só valida tokens. Não emite token, não guarda senha e não tem tela de login.

- **Por quê Keycloak e `client_credentials`:** é a recomendação do enunciado, e a comunicação aqui é sempre entre serviços, sem usuário humano.
- **Base:** "Recomenda-se **Keycloak** no Docker Compose e `client_credentials` para comunicação entre serviços." e "Cadastro de senhas e emissão própria de tokens estão fora do escopo."

### 10.2. Validação do token

Feita com a biblioteca `coreos/go-oidc`. Um token só é aceito se passar nas quatro conferências:

| Conferência | Recusa |
| --- | --- |
| assinatura, com as chaves públicas do realm | token adulterado, assinado por outra chave ou sem assinatura |
| emissor (`iss`) igual ao realm configurado | token de outro realm ou de outro IdP |
| audiência (`aud`) contém `wallet-service` | token emitido para outro serviço |
| validade (`exp`) | token expirado |

As chaves públicas são buscadas no Keycloak no primeiro uso e guardadas em memória; o serviço sobe mesmo com o IdP fora do ar.

| Situação | Erro | Resposta HTTP |
| --- | --- | --- |
| token ausente ou inválido | `auth.ErrInvalidToken` | `401` |
| chaves do IdP inacessíveis e necessárias para decidir | `app.ErrIdPUnavailable` | `503` |

Um token nunca é aceito sem validação. Com as chaves já em memória, o serviço continua aceitando tokens válidos enquanto o IdP estiver fora do ar.

- **Limitação:** quando a assinatura de um token não confere com nenhuma chave em memória, a biblioteca busca as chaves de novo, porque pode ter havido troca de chave. Se o IdP estiver fora do ar nesse momento, a resposta é `503` em vez de `401`. Nos dois casos a requisição é negada. Cada token com assinatura inválida custa uma consulta de chaves ao Keycloak.
- **Ambiente local:** o realm aceita HTTP sem TLS (`sslRequired: none`), o que só é adequado para a stack local.

Os testes de integração rodam contra um Keycloak real, com o mesmo arquivo de realm do Compose: token válido, adulterado, expirado, de outro realm, com emissor diferente do configurado, com outra audiência, sem assinatura, e IdP parado com e sem chaves em memória.

- **Limitação:** para distinguir "IdP inacessível" de "token inválido", o código procura o trecho `fetching keys` na mensagem de erro da biblioteca, que não oferece um tipo de erro para isso. O teste de integração com o Keycloak parado acusa se uma versão futura mudar o texto.

### 10.3. Identidade e permissões

| Dado | De onde vem |
| --- | --- |
| provedor autorizado | claim `provider_id`, posta no token por um mapper do cliente no Keycloak. Um token sem a claim não é de provedor. O provedor nunca é deduzido de `azp` ou `client_id` |
| permissões | escopos OAuth na claim `scope` |

| Rota | Escopo | Regra adicional |
| --- | --- | --- |
| `POST /wallets` | `wallets:write` | |
| `GET /wallets/:id`, `GET /wallets/:id/ledger` | `wallets:read` | |
| `POST /wallets/:id/reconciliation` | `wallets:read` | não altera dados |
| `POST /wagering/transactions` | `wagering:write` | o `providerId` do corpo tem de ser o da claim |
| `GET` de transações | `wagering:read` | provedor só vê as suas |
| `/health/*`, `/metrics` | nenhum | públicas |

| Cliente no realm | Escopos | `provider_id` |
| --- | --- | --- |
| `provider-a`, `provider-b` | `wagering:write`, `wagering:read` | o próprio |
| `wallet-internal` | `wallets:write`, `wallets:read`, `wagering:read` | não tem |

Assim um provedor não abre nem consulta carteira, e o serviço interno não envia aposta. Cada cliente tem `fullScopeAllowed` falso e recebe só os escopos listados.

- **Base:** "A identidade autenticada deve determinar o `providerId` autorizado. Provedores acessam apenas suas próprias transações, inclusive em replays; operações de carteira são restritas ao serviço interno."

### 10.4. Isolamento entre provedores

| Situação | Resposta | Por quê |
| --- | --- | --- |
| corpo de `POST /wagering/transactions` com `providerId` de outro provedor | `403` `PROVIDER_MISMATCH`, antes de qualquer leitura | o erro está na própria requisição |
| caminho `/providers/{outro}/...` | `403`, antes de qualquer leitura | idem |
| `GET /wagering/transactions/{id}` de uma transação de outro provedor ou de uma `OPENING` | `404`, igual ao de um identificador inexistente | não revelar que o identificador existe |
| replay com a chave ou o identificador externo de outro provedor | tratado como operação nova | as duas tuplas de idempotência incluem o provedor (seção 4.2) |

O serviço interno, que tem `wagering:read` e não tem `provider_id`, consulta transações de qualquer provedor e as internas.

A regra está em dois métodos de `app.Caller`: `CanSubmitAs` e `CanReadProvider`.

### 10.5. Segredos

O arquivo do realm não tem valor de segredo: cada cliente declara `"secret": "${VARIAVEL}"`, e o Keycloak troca o marcador pela variável de ambiente ao importar. Os valores vêm do `.env`, que não é versionado (grupo 18).

### 10.6. Acesso à fila

No SQS não há token: o controle é do broker, por credenciais e pela política da fila (grupo 15). O `providerId` de uma mensagem vem do corpo e passa pelas mesmas validações de domínio de uma requisição HTTP.

- **Base:** "O acesso à mensageria deve ser controlado por credenciais e políticas do broker, preservando as validações de domínio no consumidor."

## 11. Composição com Fx e shutdown

_Pendente (grupo 17 do `tasks.md`)._

## 12. Contrato HTTP: códigos e corpos de erro

Implementado em `internal/infra/httpapi`, com `net/http` e o `http.ServeMux` da biblioteca padrão. O enunciado trata do assunto na seção 9.

### 12.1. Rotas

| Rota | Escopo |
| --- | --- |
| `POST /wallets` | `wallets:write` |
| `GET /wallets/{walletId}` | `wallets:read` |
| `GET /wallets/{walletId}/ledger?cursor=...&limit=50` | `wallets:read` |
| `POST /wallets/{walletId}/reconciliation` | `wallets:read` |
| `POST /wagering/transactions` | `wagering:write` |
| `GET /wagering/transactions/{transactionId}` | `wagering:read` |
| `GET /providers/{providerId}/wagering/transactions/{externalTransactionId}` | `wagering:read` |
| `GET /health/live`, `GET /health/ready`, `GET /metrics` | públicas |

A tabela de rotas é uma lista no código, com o escopo ao lado de cada rota. Um teste percorre a lista e falha se alguma rota fora das três públicas não tiver escopo, e confere que cada uma responde `401` sem token.

Toda requisição passa, nesta ordem, por: identificador de correlação, recuperação de pânico, prazo de processamento, autenticação com conferência do escopo, e o handler.

### 12.2. Status por situação

| Situação | Status | Corpo |
| --- | --- | --- |
| operação processada, ou replay de uma processada | `200` | resultado da transação |
| carteira criada | `201` | carteira |
| espera por referência (processamento pendente) | `202` | resultado da transação, com o prazo da espera |
| entrada inválida | `400` | erro |
| sem token, ou token inválido | `401` | erro |
| sem o escopo, ou provedor divergente | `403` | erro |
| carteira ou transação inexistente, ou de outro provedor | `404` | erro |
| conflito de idempotência, ou carteira duplicada | `409` | erro |
| rejeição de negócio | `422` | resultado da transação, com o `failureCode` |
| erro inesperado | `500` | erro |
| indisponibilidade transitória | `503`, com `Retry-After` | erro |

- **Replay mantém o status:** o reenvio de uma operação rejeitada responde `422` de novo, com `idempotentReplay: true`; o de uma pendente, `202`.
- **Por quê `422` e não `409` para rejeição:** conflito é sobre a requisição (chave reutilizada); rejeição é o resultado definitivo de uma operação bem formada. Os dois precisam ser distinguíveis.
- **Transação `FAILED`:** só aparece em replay ou consulta de uma pendência que falhou; o envio responde `500` com o resultado da transação. O resultado é terminal: reenviar a mesma chave devolve sempre a mesma resposta.
- **Base:** "Documente os códigos HTTP e os corpos de resposta para entrada inválida, conflito, rejeição de negócio, processamento pendente e indisponibilidade transitória. Essas situações precisam ser distinguíveis pelo contrato."

### 12.3. Corpo de erro

Os erros usam `application/problem+json` (RFC 9457), com exatamente cinco campos. O `code` é estável e é o que o cliente deve usar para decidir; o corpo nunca traz mensagem interna, valor financeiro nem dado de outra carteira.

```json
{
  "type": "about:blank",
  "title": "Conflict",
  "status": 409,
  "code": "IDEMPOTENCY_KEY_REUSED",
  "correlationId": "0192f2a0-5b7c-7c11-9d0e-3f1a2b4c5d6e"
}
```

Entrada inválida (`400`):

```json
{
  "type": "about:blank",
  "title": "Bad Request",
  "status": 400,
  "code": "INVALID_MONEY",
  "correlationId": "0192f2a0-5b7c-7c11-9d0e-3f1a2b4c5d6e"
}
```

Indisponibilidade transitória (`503`, com o header `Retry-After: 1`):

```json
{
  "type": "about:blank",
  "title": "Service Unavailable",
  "status": 503,
  "code": "SERVICE_UNAVAILABLE",
  "correlationId": "0192f2a0-5b7c-7c11-9d0e-3f1a2b4c5d6e"
}
```

| Situação | Status | `code` |
| --- | --- | --- |
| entrada inválida | `400` | os códigos da seção 13.2, por exemplo `INVALID_MONEY` |
| conflito | `409` | `IDEMPOTENCY_KEY_REUSED`, `TRANSACTION_ALREADY_REGISTERED`, `WALLET_ALREADY_EXISTS` |
| indisponibilidade transitória | `503` | `SERVICE_UNAVAILABLE` |
| acesso | `401`, `403` | `UNAUTHORIZED`, `INSUFFICIENT_SCOPE`, `PROVIDER_MISMATCH` |
| não encontrado | `404` | `WALLET_NOT_FOUND`, `TRANSACTION_NOT_FOUND`, `NOT_FOUND` (rota) |
| método não permitido | `405` | `METHOD_NOT_ALLOWED` |
| erro inesperado | `500` | `INTERNAL_ERROR` |

### 12.4. Corpo do resultado de uma operação

Usado em `200`, `202` e `422`. Campos ausentes são omitidos.

Operação processada (`200`):

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "PROCESSED",
  "balance": { "amount": "975.00", "currency": "BRL" },
  "idempotentReplay": false
}
```

Processamento pendente (`202`):

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "PENDING_REFERENCE",
  "referenceExpiresAt": "2026-01-01T12:05:00Z",
  "idempotentReplay": false
}
```

Rejeição de negócio (`422`):

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "REJECTED",
  "failureCode": "INSUFFICIENT_FUNDS",
  "idempotentReplay": false
}
```

O `balance` é o saldo observado no processamento original, e é o mesmo no replay. Uma rejeição não traz `balance`.

### 12.5. Validação da entrada

- O corpo é lido com limite de tamanho e recusa campo desconhecido e conteúdo depois do JSON: `MALFORMED_REQUEST`.
- Campo obrigatório ausente: `MALFORMED_REQUEST`. Identificador malformado: `INVALID_IDENTIFIER`.
- `money.amount` tem de ser string no formato da seção 1.3; número JSON, `"25"`, valor negativo ou moeda desconhecida dão `INVALID_MONEY`.
- `Idempotency-Key` ausente ou vazio: `MISSING_IDEMPOTENCY_KEY`. A chave é usada exatamente como chegou.
- `kind` desconhecido: `UNSUPPORTED_KIND`. `OPENING` também, recusado pelo domínio.

Nada disso grava no banco, e a mesma operação pode ser reenviada depois de corrigida.

Limitações herdadas da biblioteca padrão: num corpo com chave repetida vale a última; o nome do campo é aceito em qualquer caixa; o `Content-Type` da requisição não é conferido; e um caminho com `//` ou `..` recebe o redirecionamento do `ServeMux` em vez de um corpo de erro. Nenhuma delas contorna a autenticação nem o hash, que usam sempre o valor já lido.

Num erro `500` o log traz o texto do erro interno, que é o único diagnóstico disponível; as camadas de baixo não põem segredo nem valor monetário nesse texto (seção 2.3). Num pânico o log traz só o caminho da requisição.

### 12.6. Correlação e prazo

- **`X-Correlation-Id`:** aceito quando tem de 1 a 128 caracteres visíveis; caso contrário um novo é gerado. É devolvido no header da resposta, incluído no corpo de erro, em toda linha de log da requisição e no `correlationId` dos eventos.
- **Prazo:** cada requisição tem um prazo de processamento. Se ele vence, ou se o cliente desconecta, o contexto é cancelado, a transação SQL é desfeita e a resposta é `503`. Nada fica gravado pela metade.

### 12.7. Leituras

- **Ledger:** em ordem crescente da versão da carteira, `limit` de 1 a 200 (padrão 50). O `nextCursor` é opaco (a última versão vista, em base64) e só aparece quando há mais páginas. Como a ordem é pela versão, um lançamento criado durante a navegação aparece nas páginas seguintes e nenhum é repetido.
- **Transação:** devolve identificadores, tipo, valor, estado, `failureCode`, saldo resultante, prazo da espera e instantes de criação e conclusão, quando existem.
- **Base:** "A paginação do ledger deve usar cursor opaco e ordenação estável. As consultas de transação devem permitir acompanhar pendências e consultar códigos de rejeição ou falha."

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

Implementado em `internal/infra/observability`. O enunciado trata do assunto na seção 12. A rota `/metrics` e as de saúde são montadas no grupo 14 do `tasks.md`; a ligação com o PostgreSQL e o SQS é do grupo 17.

### 14.1. Logs

Logs em JSON, uma linha por registro, com `log/slog` da biblioteca padrão. Cada linha tem `time` (UTC, RFC 3339), `level` e `msg`.

| Campo | Quem o coloca |
| --- | --- |
| `correlationId` | o middleware HTTP ou o consumidor SQS guardam no `context.Context`; o logger acrescenta a toda linha escrita com esse contexto |
| `messageId` | o consumidor SQS, do mesmo jeito |
| `transactionId`, `walletId`, `providerId` | o caso de uso, na linha que conclui a operação |

O que nunca é logado:

- valores monetários: valor da operação, saldo, diferença de reconciliação;
- o corpo de requisições, mensagens ou eventos;
- tokens e credenciais. Segredos de configuração usam o tipo `Secret`, que escreve `[REDACTED]` em `String`, `%s`, `%v`, `%+v`, `%#v`, JSON e `slog`; o valor só sai por `Reveal()`, chamado onde a conexão é aberta.

Limitações do `Secret`: ele não protege contra verbos numéricos do `fmt` (`%d`, `%x` sobre o campo interno) nem quando está num campo **não exportado** de outra struct, caso em que o `fmt` não consegue chamar seus métodos. Por isso os campos de segredo da configuração são exportados.

Um identificador posto no contexto não deve ser passado de novo como argumento do log: a chave sairia duas vezes na linha.

Os testes conferem isso: o do caso de uso processa uma aposta, um replay e uma rejeição e procura os valores nas linhas de log; `logtest.AssertNoLeak` faz a mesma checagem nos testes de integração.

- **Base:** "Produza logs JSON com os identificadores disponíveis para rastrear a operação: `correlationId`, `messageId`, `transactionId`, `walletId` e `providerId`. Não registre credenciais, dados sensíveis ou payloads financeiros completos."

### 14.2. Métricas

Formato Prometheus, com `prometheus/client_golang`, expostas em `GET /metrics`. A camada de aplicação conhece só a interface `app.Metrics`.

| Métrica | Tipo | Rótulos | O que o enunciado pede |
| --- | --- | --- | --- |
| `wager_transactions_total` | contador | `channel`, `kind`, `status`, `failure_code` | resultados por status |
| `duplicates_total` | contador | `source` (`replay`, `inbox`) | duplicatas |
| `retries_total` | contador | `component` | retries |
| `dlq_messages_total` | contador | `reason` | DLQ |
| `concurrency_conflicts_total` | contador | `type` (`unique_violation`, `lock_timeout`) | conflitos de concorrência |
| `outbox_lag_seconds` | medidor | | atraso da outbox |
| `processing_duration_seconds` | histograma | `channel` | latência de processamento |
| `reconciliation_divergences_total` | contador | | divergências de reconciliação |

Os rótulos vêm só de conjuntos fechados: canal, tipo, estado, código de falha, componente. Nenhum identificador de carteira, jogador, transação ou provedor e nenhum valor monetário vira rótulo.

Um replay conta em `duplicates_total` e não em `wager_transactions_total`. Uma transação `FAILED` aparece em `wager_transactions_total` com `status="FAILED"`.

- **Base:** "Exponha métricas para resultados por status, duplicatas, retries, DLQ, conflitos de concorrência, atraso da outbox, latência de processamento e divergências de reconciliação."

### 14.3. Saúde

| Rota | Resposta |
| --- | --- |
| `GET /health/live` | `200` enquanto o processo estiver de pé; não consulta nenhuma dependência |
| `GET /health/ready` | `200` quando todas as checagens passam; `503` com o nome das que falharam; `503` durante o encerramento |

A resposta de readiness traz só o nome da dependência (`postgres`, `sqs`), nunca o erro de conexão.

- **Base:** "Liveness do processo e readiness de PostgreSQL e SQS."

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

Os casos de uso são métodos de um único tipo, `app.Service`, que recebe as portas como campos. Eles compartilham quase todas as dependências; um tipo por caso de uso só acrescentaria montagem.

Toda busca que não encontra devolve `app.ErrNotFound`. Falhas passageiras da infraestrutura chegam como `app.ErrTransient`.

`internal/app/apptest` tem implementações em memória dessas portas, usadas só nos testes unitários dos casos de uso. Elas imitam as regras de unicidade do banco e desfazem as escritas quando a transação falha, inclusive o bloco de dentro de um `Run` aninhado. Não imitam tudo: chaves estrangeiras, o prazo de reserva da outbox e a recusa de escrita em `RunReadOnly` só existem no banco real. Os testes de integração usam PostgreSQL real.

- **Base:** "O domínio deve permanecer independente de Fx, HTTP, SQS e bibliotecas de persistência." e, na seção 13, "Não substitua toda a infraestrutura por mocks."
