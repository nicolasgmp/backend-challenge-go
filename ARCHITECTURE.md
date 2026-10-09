# Arquitetura

Decisões técnicas da solução, na ordem dos temas que o enunciado (`docs/challenge.md`) pede. O que é interpretação ou escolha própria está marcado; a seção 15 reúne interpretações, limitações e o que não foi feito.

Camadas: `domain` (regras, sem dependência externa) ← `app` (casos de uso e interfaces) ← `infra` (PostgreSQL, SQS, HTTP, OIDC) ← `bootstrap` (composição com Fx). Um teste (`internal/archtest`) falha se o domínio importar Fx, `net/http`, SDK da AWS ou pgx, ou se `app` importar `infra`.

## 1. Dinheiro

- **Representação:** `Money` é um `int64` em unidades mínimas (centavos) mais o código ISO 4217. Nenhum `float32`/`float64` em parsing, cálculo, JSON ou banco; `make nofloat` confere.
- **Limites:** de `-92233720368547758.08` a `92233720368547758.07`. Parsing, soma, subtração e negação devolvem erro de overflow em vez de dar a volta.
- **Formato de entrada:** exatamente `dígitos.dois dígitos` (`25.00`). São recusados: vazio, `25`, `25.0`, `25.000`, `025.00`, `1e2`, `NaN`, `Infinity`, sinal `+`, espaços. Nada é arredondado. Valor negativo é recusado nas entradas externas.
- **Normalização antes do hash:** não há; cada valor tem uma única escrita aceita.
- **Moedas:** lista fechada `BRL`, `USD`, `EUR`. Soma, subtração e comparação exigem a mesma moeda.
- **JSON:** `{"amount":"25.00","currency":"BRL"}`, com `amount` sempre em string.
- **Banco:** `BIGINT` (unidades mínimas) mais `CHAR(3)`; somas do ledger são feitas em inteiros.

## 2. Banco e transações

- **Biblioteca:** `pgx` v5 com SQL escrito à mão. Sem ORM nem gerador de código.
- **Delimitação da transação:** quem abre é o caso de uso, por `TxRunner.Run`. A transação vai no `context.Context`, e todo repositório chamado dentro dela a usa. Erro ou pânico desfaz tudo. Nos repositórios, escrita ou lock fora de transação devolve erro; só as marcações do publicador da outbox rodam fora, de propósito.
- **`Run` dentro de `Run`:** vira um `SAVEPOINT`. É o caso do consumidor SQS, que grava a inbox e chama o caso de uso na mesma transação.
- **Leitura consistente:** `RunReadOnly` usa `REPEATABLE READ READ ONLY` (reconciliação).
- **Erros:** violação de índice único vira `app.ErrUniqueViolation`; deadlock, falha de serialização, `lock_timeout` e perda de conexão viram `app.ErrTransient`. A mensagem nunca traz a linha recusada nem a senha.

O que o banco impõe, independentemente do código Go:

| Tabela | Regras |
| --- | --- |
| `wallets` | saldo ≥ 0; versão ≥ 1; uma carteira por `(player_id, currency)` |
| `wager_transactions` | únicos `(provider_id, idempotency_key)` e `(provider_id, external_transaction_id)`; um `OPENING` por carteira; uma reversão `PROCESSED` por referência; reversão `PROCESSED` exige a referência resolvida; origem externa exige os dados do provedor e origem interna os proíbe; tipo conhecido; valor ≥ 0; nunca `PENDING` |
| `wallet_ledger_entries` | valor > 0; saldos ≥ 0; `balance_after = balance_before ± amount`; únicos `(wallet_id, transaction_id)` e `(wallet_id, wallet_version)`; triggers bloqueiam `UPDATE`, `DELETE` e `TRUNCATE` |
| `inbox` | chave `(consumer_name, message_id)` |
| `outbox` | trigger bloqueia alteração das colunas do evento |

Migrations em `migrations/`, uma por tabela, com `up` e `down`; um teste aplica, reverte até o banco vazio e reaplica.

## 3. Concorrência e locks

Estratégia: **lock pessimista por carteira**. Toda operação que altera saldo trava a linha da carteira com `SELECT ... FOR UPDATE` antes de ler transações, ledger ou outbox (no SQS, só o registro da inbox vem antes):

```
BEGIN
  SET lock_timeout (só nesta transação; 5 s por padrão)
  SELECT wallets ... FOR UPDATE            <- primeiro
  busca por chave e por operação           (replay ou conflito)
  resolve a referência, se houver
  aplica no agregado                       (débito, crédito ou nada)
  INSERT wager_transactions
  UPDATE wallets                           <- só se houve movimentação
  INSERT wallet_ledger_entries             <- só se houve movimentação
  INSERT outbox
COMMIT
```

- Operações da mesma carteira entram em fila; carteiras diferentes não se esperam. Não há lock global nem em memória, então vale entre instâncias.
- A mesma trava põe em fila a checagem de idempotência e a de reversão anterior. Por isso não se usou controle otimista nem `UPDATE` condicionado.
- Lock não obtido no prazo: a operação falha como indisponibilidade transitória, sem gravar nada.
- **Disputa que o lock não cobre:** a mesma chave enviada ao mesmo tempo para duas carteiras diferentes. O índice único deixa passar uma; a outra desfaz a transação e responde indisponibilidade transitória (`503`, ou retry no SQS). No reenvio ela encontra a linha vencedora e recebe o conflito.
- As constraints da seção 2 são a segunda barreira: valem mesmo que o lock não seja tomado.

## 4. Idempotência

- **Identidades, ambas por provedor:** a chave (`Idempotency-Key` no HTTP, `data.idempotencyKey` no SQS) e a operação (`externalTransactionId`). A chave é gravada como chegou, nunca substituída.
- **Persistência:** chave, hash e resultado ficam na própria linha de `wager_transactions`. Sobrevive a reinício e vale em qualquer instância.
- **Hash:** SHA-256 em hexadecimal de um JSON canônico (chaves em ordem alfabética, sem espaços) com `externalTransactionId`, `gameId`, `kind`, `money`, `playerId`, `providerId`, `referenceExternalTransactionId` (se houver), `roundId` e `walletId`. Ficam de fora a chave e os metadados de transporte (`messageId`, `occurredAt`, headers, `correlationId`).
- **Equivalência HTTP e SQS:** o JSON é escrito a partir dos valores já validados, pela mesma função nos dois canais; a ordem e o espaçamento do corpo original não importam. Os UUID são reescritos em minúsculas; os demais identificadores são comparados como chegaram.

| Chave existe | Operação existe | Hash | Resultado |
| --- | --- | --- | --- |
| não | não | | processa |
| sim | | igual | replay: resultado gravado, `idempotentReplay: true`, nada é reaplicado |
| sim | | diferente | conflito `IDEMPOTENCY_KEY_REUSED` |
| não | sim | | conflito `TRANSACTION_ALREADY_REGISTERED` |

O replay devolve o saldo observado no processamento original (`result_balance`), mesmo que a carteira já tenha mudado. Uma entrada inválida não grava nada e pode ser reenviada corrigida com a mesma chave.

## 5. Máquina de estados e falhas

```
PENDING ──────────────> PROCESSED
   │ ──────────────────> REJECTED
   └──> PENDING_REFERENCE ──> PROCESSED
                          ──> REJECTED
                          ──> FAILED
```

- As transições são métodos do agregado (`MarkProcessed`, `MarkRejected`, `MarkPendingReference`, `MarkFailed`); qualquer outra devolve `ErrInvalidTransition`. `PROCESSED`, `REJECTED` e `FAILED` são terminais: nenhum método do agregado sai deles.
- **`PENDING` nunca é gravado:** operação sem dependência é concluída num único commit.
- **Criação e reidratação separadas:** `NewExternal`/`NewOpening` validam e criam; `Rehydrate` só reproduz o estado gravado.
- **`OPENING`** é interno: sem provedor, id externo, chave, hash, rodada, jogo ou referência. Enviado por HTTP ou SQS, é recusado.

| Tipo | Valor | Referência | Movimento |
| --- | --- | --- | --- |
| `BET` | > 0 | não aceita | débito |
| `WIN` | > 0 | opcional (um `BET` da mesma rodada) | crédito |
| `LOSS` | = 0 | não aceita | nenhum; sem ledger, sem mudança de versão |
| `REFUND` | > 0 | obrigatória | crédito |
| `ROLLBACK` | > 0 | obrigatória | contrário ao original |

**Transitório × permanente:**

| Erro | Tratamento |
| --- | --- |
| transitório (conexão, timeout, deadlock, `lock_timeout`) | nada é gravado. HTTP responde `503`; SQS adia a mensagem; a pendência é tentada no ciclo seguinte |
| inesperado ao resolver uma pendência | conta; no quinto seguido a transação vira `FAILED` com `PROCESSING_FAILED`, sem evento |

`FAILED` só existe para pendência de referência. Numa operação síncrona, a falha de infraestrutura desfaz tudo e o cliente pode reenviar.

## 6. Reversões

| Reversão | Pode referenciar | Movimento |
| --- | --- | --- |
| `REFUND` | `BET` | crédito |
| `ROLLBACK` | `BET` | crédito |
| `ROLLBACK` | `WIN` ou `REFUND` | débito |

- A referência é buscada por `(providerId, referenceExternalTransactionId)`, com o provedor da própria operação.
- Validação, nesta ordem: referência `PROCESSED` (senão `REFERENCE_NOT_PROCESSED`); tipo permitido (senão `REFERENCE_KIND_NOT_ALLOWED`); mesmo provedor, jogador, carteira, moeda, rodada e valor (senão `REFERENCE_MISMATCH`). Reversão parcial não existe. `WIN` com referência não confere valor.
- **`REFUND` + `ROLLBACK` da mesma aposta:** cada operação aceita no máximo **uma** reversão `PROCESSED`, de qualquer tipo, imposta por índice único. A segunda é rejeitada com `REFERENCE_ALREADY_REVERSED`. Isso impede devolver o mesmo débito duas vezes. Reversão rejeitada não ocupa a vaga.
- **Reversão sem saldo:** `REVERSAL_INSUFFICIENT_FUNDS`, diferente do `INSUFFICIENT_FUNDS` de uma aposta.

## 7. Referências pendentes

- Reversão que chega antes da referência é gravada como `PENDING_REFERENCE`, sem movimentação, com evento `WagerTransactionPendingReference`.
- **Limite:** prazo (TTL) de 5 minutos, configurável (`PENDING_REFERENCE_TTL`).
- **Worker:** a cada segundo lista as pendências vencidas e reavalia cada uma na sua própria transação, com o mesmo lock de carteira e as mesmas validações. Backoff: 1, 2, 4, 8, 16 e 30 segundos, nunca além do prazo. Estado, prazo e próxima tentativa ficam no banco, então qualquer instância retoma depois de um reinício.

| O que a tentativa encontra | Resultado |
| --- | --- |
| referência `PROCESSED` | aplica a operação |
| referência `REJECTED` ou `FAILED` | `REJECTED` com `REFERENCE_NOT_PROCESSED`, sem esperar |
| referência ausente ou também pendente, antes do prazo | reagenda |
| idem, no prazo ou depois | `REJECTED` com `REFERENCE_NOT_FOUND` e evento de rejeição |

A busca é feita antes de olhar o prazo: se a referência chegou enquanto o serviço estava parado, a pendência é resolvida em vez de expirar.

## 8. Inbox e consumidor SQS

- **Filas:** `wager-transactions.fifo` e `wager-transactions-dlq.fifo`; visibility timeout de 30 s; redrive após 5 recebimentos. Criadas por `cmd/queues`, que pode rodar várias vezes.
- **Produtor:** `MessageGroupId` = `walletId`, `MessageDeduplicationId` = `messageId`. A correção não depende disso: lock, inbox e idempotência cobrem.
- **Mesmo caso de uso do HTTP,** com `data.idempotencyKey` como chave.

```
recebe -> lê o envelope
  inválido ------------------------> DLQ com o motivo, apaga
BEGIN
  INSERT inbox (consumidor, messageId, SHA-256 do corpo)
    já existe, mesmo hash ---------> COMMIT, apaga (duplicata)
    já existe, outro hash ---------> ROLLBACK, DLQ, apaga
  caso de uso
    entrada inválida ou conflito --> ROLLBACK, DLQ, apaga
  UPDATE inbox SET completed_at
COMMIT
apaga a mensagem
erro transitório ------------------> ROLLBACK, adia a visibilidade
```

- Inbox, domínio, ledger e eventos são confirmados no mesmo commit. A mensagem só é apagada depois dele; se o processo morre no meio, a reentrega é reconhecida pela inbox.
- Rejeição de negócio e referência pendente apagam a mensagem (resultado gravado); não vão para a DLQ.
- **Mensagens inválidas** (JSON inválido, envelope incompleto, `type` desconhecido, entrada inválida, conflito) vão direto à DLQ, com o motivo no atributo `reason`, sem esperar o redrive, para não segurar a fila FIFO da carteira.
- **Retry:** `ChangeMessageVisibility` com atraso que dobra a cada recebimento; no quinto a visibilidade é zerada e a entrega seguinte é o redrive do broker.
- **Motivos na DLQ (`reason`):** `INVALID_MESSAGE`, `UNKNOWN_MESSAGE_TYPE`, `MESSAGE_ID_REUSED`, ou o código de entrada inválida ou de conflito.
- **`SIGTERM`:** para de buscar, conclui o que está em tratamento e libera a visibilidade do que falhar.

## 9. Outbox e eventos

- Os eventos são gravados na tabela `outbox` na mesma transação da mudança. Nenhum caso de uso fala com o SQS: a publicação só pode acontecer depois do commit.
- **Publicador (worker separado):** reserva um lote com `FOR UPDATE SKIP LOCKED` gravando `locked_until` (30 s), envia fora de transação SQL e marca `published_at`.
- **Vários publicadores:** cada um pula as linhas reservadas pelo outro.
- **Falha:** conta a tentativa e reagenda (1 s dobrando até 60 s). Não há limite: evento confirmado no banco nunca é descartado.
- **Recuperação:** se o publicador morre antes de publicar, o evento segue pendente; se morre depois de publicar e antes de marcar, a reserva vence e outro republica com o mesmo `eventId`.

| Evento | Quando |
| --- | --- |
| `WagerTransactionProcessed` | operação concluída, inclusive `LOSS` e `OPENING` |
| `WagerTransactionRejected` | rejeição de negócio |
| `WalletBalanceChanged` | mudança efetiva de saldo |
| `WagerTransactionPendingReference` | registro da espera |

- **Envelope:** `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId` (opcional; o `messageId` quando veio do SQS), `occurredAt` (UTC, RFC 3339), `version`, `data`. Tipo e versão são fixados pelo construtor do evento.
- **Roteamento:** `wallet-events.fifo` (com DLQ); `MessageGroupId` = carteira; `MessageDeduplicationId` = `eventId`.
- **Contrato de consumo:** entrega at-least-once; deduplicar por `eventId`. Com mais de um publicador a ordem não é garantida; use o `walletVersion` de `WalletBalanceChanged`.

## 10. Autenticação e autorização

- **IdP:** Keycloak 26.7.5 com `client_credentials`, como recomenda o enunciado; a comunicação é sempre entre serviços. O serviço só valida tokens.
- **Validação** (`coreos/go-oidc`): assinatura, emissor, audiência (`wallet-service`) e validade. Token ausente, inválido ou que não pôde ser validado: `401`. Nunca se aceita token sem validar; com o IdP fora do ar, tokens cuja chave já está em memória continuam sendo validados.
- **Provedor autorizado:** claim `provider_id`, posta por um mapper do cliente. Token sem a claim não é de provedor.
- **Permissões:** escopos OAuth, declarados ao lado de cada rota. Um teste falha se alguma rota de negócio ficar sem escopo.

| Rota | Escopo |
| --- | --- |
| `POST /wallets` | `wallets:write` |
| `GET /wallets/{id}`, `/ledger`, `POST .../reconciliation` | `wallets:read` |
| `POST /wagering/transactions` | `wagering:write` |
| `GET` de transações | `wagering:read` |
| `/health/*`, `/metrics` | públicas |

Provedores recebem só os escopos `wagering:*`; o serviço interno recebe `wallets:*` e `wagering:read`, sem `provider_id`.

| Situação | Resposta |
| --- | --- |
| corpo ou caminho com outro provedor | `403`, antes de qualquer leitura |
| transação de outro provedor (ou `OPENING`) consultada pelo id interno | `404`, igual ao de id inexistente |
| replay com chave ou id externo de outro provedor | operação nova: as tuplas de idempotência incluem o provedor |

- **Segredos:** só por variável de ambiente. O arquivo do realm traz `${VARIAVEL}`, resolvido na importação.
- **Fila:** o controle é do broker, por credenciais; o `providerId` da mensagem passa pelas mesmas validações de domínio.

## 11. Contrato HTTP

| Situação | Status |
| --- | --- |
| processada (e replay de processada) | `200` |
| carteira criada | `201` |
| processamento pendente (espera por referência) | `202` |
| entrada inválida | `400` |
| sem token ou token inválido | `401` |
| sem escopo ou provedor divergente | `403` |
| carteira ou transação inexistente | `404` |
| conflito de idempotência, carteira duplicada | `409` |
| rejeição de negócio | `422` |
| indisponibilidade transitória | `503` com `Retry-After` |

O replay repete o status original. Erro inesperado responde `500` com o código `INTERNAL_ERROR`; o replay de uma transação `FAILED` também responde `500`, com o resultado da transação.

Resultado de operação (`200`, `202`, `422`); campos ausentes são omitidos:

```json
{"transactionId":"0192f298-...","status":"PROCESSED","balance":{"amount":"975.00","currency":"BRL"},"idempotentReplay":false}
{"transactionId":"0192f298-...","status":"PENDING_REFERENCE","referenceExpiresAt":"2026-01-01T12:05:00Z","idempotentReplay":false}
{"transactionId":"0192f298-...","status":"REJECTED","failureCode":"INSUFFICIENT_FUNDS","idempotentReplay":false}
```

Erros (`400`, `401`, `403`, `404`, `409`, `503`) em `application/problem+json` (RFC 9457); o `code` é estável:

```json
{"type":"about:blank","title":"Bad Request","status":400,"code":"INVALID_MONEY","correlationId":"..."}
{"type":"about:blank","title":"Conflict","status":409,"code":"IDEMPOTENCY_KEY_REUSED","correlationId":"..."}
{"type":"about:blank","title":"Service Unavailable","status":503,"code":"SERVICE_UNAVAILABLE","correlationId":"..."}
```

- **Entrada:** corpo com limite de tamanho; campo desconhecido é recusado. Rota ou método inexistente recebe o `404`/`405` padrão do roteador.
- **Ledger:** ordenado pela versão da carteira; `limit` de 1 a 200 (padrão 50); `nextCursor` opaco (a última versão vista, em base64). Lançamento criado durante a navegação aparece nas páginas seguintes, sem repetição.
- **Reconciliação:** soma créditos menos débitos do ledger numa leitura consistente, compara com o saldo e não altera nada. Divergência vai para a resposta, o log e uma métrica.
- **Correlação:** `X-Correlation-Id` é aceito ou gerado, devolvido na resposta e propagado a logs e eventos.

## 12. Códigos de falha

A divisão: o que se julga olhando só a requisição, mais a carteira que não existe, é **entrada inválida** (nada é gravado; pode ser corrigido e reenviado). O que depende do estado gravado é **rejeição** (transação `REJECTED`, definitiva).

| Entrada inválida | Situação |
| --- | --- |
| `MALFORMED_REQUEST` | JSON inválido, campo ausente ou desconhecido |
| `INVALID_IDENTIFIER` | identificador malformado |
| `INVALID_MONEY` | valor ou moeda fora do formato |
| `MISSING_IDEMPOTENCY_KEY` | chave ausente |
| `UNSUPPORTED_KIND` | `OPENING` ou tipo desconhecido |
| `INVALID_AMOUNT_FOR_KIND` | política de zero violada |
| `MISSING_REFERENCE`, `UNEXPECTED_REFERENCE` | referência faltando ou sobrando |
| `INVALID_CURSOR` | cursor do ledger inválido |
| `WALLET_NOT_FOUND`, `TRANSACTION_NOT_FOUND` | recurso inexistente (`404`) |

| Rejeição (`failureCode`) | Situação |
| --- | --- |
| `INSUFFICIENT_FUNDS` | aposta maior que o saldo |
| `REVERSAL_INSUFFICIENT_FUNDS` | reversão que debitaria além do saldo |
| `BALANCE_OVERFLOW` | crédito além do limite de `int64` |
| `PLAYER_WALLET_MISMATCH`, `CURRENCY_MISMATCH` | jogador ou moeda diferentes dos da carteira |
| `REFERENCE_NOT_FOUND` | a referência não chegou no prazo |
| `REFERENCE_NOT_PROCESSED` | a referência terminou sem sucesso |
| `REFERENCE_MISMATCH` | dados divergentes da referência |
| `REFERENCE_KIND_NOT_ALLOWED` | tipo da referência não pode ser desfeito |
| `REFERENCE_ALREADY_REVERSED` | a referência já tem reversão processada |

Falha: `PROCESSING_FAILED` (transação `FAILED`).

Conflitos (`409`): `IDEMPOTENCY_KEY_REUSED`, `TRANSACTION_ALREADY_REGISTERED`, `WALLET_ALREADY_EXISTS`. Acesso: `UNAUTHORIZED` (`401`), `INSUFFICIENT_SCOPE` e `PROVIDER_MISMATCH` (`403`). Indisponibilidade: `SERVICE_UNAVAILABLE` (`503`).

## 13. Fx e shutdown

- `cmd/server/main.go` só chama `fx.New`. A configuração é lida antes e entregue ao Fx; depois há um módulo por área em `internal/bootstrap`: observabilidade, postgres, sqs, auth, app, workers, http. Injeção por construtor.
- **Configuração:** só por variável de ambiente, validada antes de qualquer conexão. O erro cita o nome da variável, nunca o valor. Segredos usam um tipo que imprime `[REDACTED]`.
- **Inicialização:** verifica PostgreSQL, filas SQS e chaves do IdP. Se algo falha, o processo termina com código diferente de zero.
- **Encerramento** (ordem inversa dos ganchos do `fx.Lifecycle`):
  1. readiness passa a `503`;
  2. o servidor HTTP para de aceitar conexões e espera as requisições em andamento;
  3. consumidor, publicador da outbox e worker de referências são cancelados; cada gancho espera o `Run` retornar, e cada worker registra em log que parou;
  4. conexões do SQS e pool do banco são fechados.
- **Prazo:** 25 s (`SHUTDOWN_TIMEOUT`). Se vencer, o processo termina; nada parcial é confirmado, porque transação sem commit é desfeita pelo banco.
- Testes: `fx.ValidateApp` sem Docker; e a aplicação inteira iniciada e encerrada duas vezes com dependências reais, conferindo a ordem pelos logs e a ausência de goroutines restantes.

## 14. Observabilidade

- **Logs:** JSON (`log/slog`), em UTC, com `correlationId`, `messageId`, `transactionId`, `walletId` e `providerId` quando disponíveis. Nunca valores monetários, corpos de requisição ou mensagem, tokens ou credenciais.
- **Métricas** (Prometheus, `GET /metrics`), só com rótulos de conjuntos fechados:

| Métrica | Pedido do enunciado |
| --- | --- |
| `wager_transactions_total` (`channel`, `kind`, `status`, `failure_code`) | resultados por status |
| `duplicates_total` (`source`) | duplicatas |
| `retries_total` (`component`) | retries |
| `dlq_messages_total` (`reason`) | DLQ |
| `concurrency_conflicts_total` (`type`) | conflitos de concorrência |
| `outbox_lag_seconds` | atraso da outbox |
| `processing_duration_seconds` (`channel`) | latência |
| `reconciliation_divergences_total` | divergências de reconciliação |

- **Saúde:** `/health/live` não consulta dependências; `/health/ready` verifica PostgreSQL e SQS e responde `503` com o nome do que falhou, e também durante o encerramento.

## 15. Interpretações, limitações e trabalho não concluído

**Interpretações adotadas**

- A linha entre entrada inválida e rejeição (seção 12) e os nomes dos códigos.
- Jogador ou moeda divergentes da carteira viram rejeição gravada, para que quem envia pela fila receba o evento.
- Operação conhecida enviada com outra chave é conflito, não replay.
- No máximo uma reversão processada por operação, de qualquer tipo (o enunciado exige ao menos "do mesmo tipo").
- `FAILED` só para pendência que falha cinco vezes seguidas; não gera evento.
- TTL em vez de número máximo de tentativas para a referência pendente.
- `PENDING` nunca é gravado (processamento síncrono).
- `404` para transação de outro provedor; `422` para rejeição; `202` para pendência.
- Mensagens inválidas vão à DLQ pelo próprio consumidor, sem esperar o redrive.

**Limitações**

- Só `BRL`, `USD` e `EUR`.
- Uma rejeição encerra o `externalTransactionId`; o provedor reenvia com outro identificador.
- Depois que o `REFUND` de uma aposta é desfeito por `ROLLBACK`, a aposta não aceita novo reembolso.
- Um `WIN` pode referenciar uma aposta já reembolsada.
- A ordem dos eventos de uma carteira não é garantida com mais de um publicador.
- O envio à DLQ e a remoção da origem não são atômicos; pode haver cópia na DLQ.
- Mensagens da mesma carteira num mesmo lote são tratadas em paralelo; a ordem entre elas não é garantida.
- O banco não confere a coerência entre linhas de tabelas diferentes; isso é do caso de uso.
- O MiniStack local aceita chamadas sem credencial e não aplica política de fila. Na AWS o controle seria uma política IAM por fila.
- A leitura de JSON usa a biblioteca padrão: chave repetida vale a última, e o nome do campo é aceito em qualquer caixa.

**Não concluído**

- Partidas dobradas, tracing, dashboards e teste de carga (opcionais no enunciado).
- TLS e usuário de banco separado do dono das tabelas.

**Onde ver cada critério de avaliação**

| Critério | Pontos | Seções | Evidência |
| --- | ---: | --- | --- |
| Integridade financeira | 20 | 1, 2, 6 | `internal/domain/money`, `migrations/`, reconciliação ao fim de cada teste de integração e ponta a ponta |
| Concorrência | 20 | 3 | `test/integration/usecase_test.go` (duas apostas de 80,00; 50 envios; 20 créditos); `test/e2e` com três instâncias |
| Idempotência | 15 | 4 | `internal/app/submit.go`; testes de replay, conflito e reinício |
| Mensageria e recuperação | 15 | 7, 8, 9 | `test/integration/messaging_test.go` (inbox, reentrega, DLQ, dois publicadores, broker fora do ar) |
| Modelagem e arquitetura | 10 | 5, 10, 13 | `internal/domain`, `internal/bootstrap`, `internal/archtest` |
| Testes | 10 | 15 | `make integration` (containers reais) e `make e2e` |
| Observabilidade | 5 | 14 | `internal/infra/observability`; `/metrics`, `/health/*` |
| Documentação | 5 | | `README.md` e este documento |

**Verificação**

- `go test -race ./...`: regras de `Money`, carteira, transições, os cinco tipos, conflito de payload, abertura.
- `make integration` (PostgreSQL, Keycloak e MiniStack reais): constraints, ledger imutável, atomicidade com falha injetada, inbox e reentrega, outbox com dois publicadores, DLQ, retry, autenticação e isolamento entre provedores, início e parada da aplicação.
- `make e2e` (três instâncias no Compose): a mesma aposta 50 vezes; duas apostas de 80,00 sobre 100,00; carteiras em paralelo; a mesma operação por HTTP e SQS; reversão antes da referência (resolvida e expirada); instância morta durante a carga; reinício com pendência aberta; PostgreSQL e SQS fora do ar. Todo cenário termina reconciliando a carteira.
