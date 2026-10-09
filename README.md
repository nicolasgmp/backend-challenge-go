# Carteira e apostas: serviço em Go com Uber Fx

Serviço que movimenta carteiras de jogadores a partir de operações de provedores de jogos (`BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`), recebidas por uma API HTTP e por uma fila SQS, com as mesmas garantias nos dois canais. O enunciado está em [`docs/challenge.md`](docs/challenge.md) e as decisões técnicas, em [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Pré-requisitos

| Ferramenta | Para quê |
| --- | --- |
| Docker com Docker Compose v2 | subir o ambiente e rodar os testes de integração e ponta a ponta |
| Go 1.27.1 | rodar os testes e compilar fora do container |
| `make` | atalhos de subida, migrations e testes (cada alvo é um comando que também pode ser rodado à mão) |
| `curl` e `python3` | seguir os exemplos deste roteiro |

Não é preciso conta externa nem token de terceiros: o SQS local é o MiniStack.

## Subir o ambiente

```sh
cp .env.example .env
docker compose up --build --wait
```

Isso sobe, a partir de um checkout limpo:

| Serviço | Porta no host | Observação |
| --- | --- | --- |
| três instâncias do serviço | `8081`, `8082`, `8083` | processos separados, cada um com suas conexões |
| PostgreSQL 17.6 | `5432` | |
| Keycloak 26.7.5 | `8090` | realm `wallet` importado na primeira subida |
| MiniStack 1.5.23 (SQS) | `4566` | |
| job `migrations` | | aplica as migrations e termina |
| job `queues` | | cria as quatro filas com redrive e termina |

O comando só retorna quando as três instâncias respondem `200` em `/health/ready`. Para derrubar tudo e apagar os dados: `docker compose down --volumes`.

`make up` e `make down` fazem o mesmo.

## Variáveis de ambiente

Todas estão no [`.env.example`](.env.example), que traz só valores locais de exemplo; o `.env` de uso real não é versionado.

| Variável | Obrigatória | Padrão | Significado |
| --- | --- | --- | --- |
| `DATABASE_URL` | sim | | conexão com o PostgreSQL (segredo) |
| `DB_LOCK_TIMEOUT` | não | `5s` | espera máxima pelo lock de uma carteira |
| `HTTP_ADDR` | não | `:8080` | endereço do servidor HTTP |
| `OIDC_ISSUER_URL` | sim | | emissor esperado nos tokens |
| `OIDC_KEYS_URL` | sim | | de onde vêm as chaves públicas do realm |
| `OIDC_AUDIENCE` | sim | | audiência esperada nos tokens |
| `SQS_ENDPOINT` | sim | | endereço do SQS |
| `AWS_REGION` | sim | | região usada para assinar as chamadas |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | sim | | credenciais do broker (segredos) |
| `PENDING_REFERENCE_TTL` | não | `5m` | quanto uma reversão espera pela operação que referencia |
| `SHUTDOWN_TIMEOUT` | não | `25s` | prazo para concluir o trabalho em andamento no encerramento |

As demais variáveis do `.env.example` (`POSTGRES_PASSWORD`, `KEYCLOAK_*`) são usadas só pelo Compose, para criar o banco e os clientes do Keycloak. O Compose recusa subir se faltar alguma delas.

Trocar um segredo do Keycloak depois da primeira subida exige recriar os dados (`docker compose down --volumes`), porque o realm só é importado uma vez.

## Migrations

As migrations ficam em [`migrations/`](migrations), numeradas, cada uma com `up` e `down`. O job `migrations` do Compose aplica todas na subida. Para aplicar ou reverter à mão, com a stack de pé:

```sh
make migrate-up      # aplica as pendentes
make migrate-down    # reverte a última
```

Os dois alvos rodam a ferramenta `golang-migrate` dentro de um container. O `Makefile` carrega o `.env`, então não é preciso exportar a `DATABASE_URL` antes.

## Filas

O job `queues` cria as filas sem passo manual. Para recriá-las (por exemplo depois de reiniciar o MiniStack, que não guarda estado):

```sh
docker compose run --rm queues
```

| Fila | Uso |
| --- | --- |
| `wager-transactions.fifo` | entrada de operações |
| `wager-transactions-dlq.fifo` | mensagens que não puderam ser processadas |
| `wallet-events.fifo` | eventos publicados pelo serviço |
| `wallet-events-dlq.fifo` | DLQ dos eventos |

## Identidades de teste

O realm é importado de [`deploy/keycloak/wallet-realm.json`](deploy/keycloak/wallet-realm.json), com três clientes `client_credentials`. Os segredos são os do seu `.env`.

| Cliente | Variável do segredo | Escopos | `provider_id` |
| --- | --- | --- | --- |
| `wallet-internal` | `KEYCLOAK_INTERNAL_CLIENT_SECRET` | `wallets:write`, `wallets:read`, `wagering:read` | não tem |
| `provider-a` | `KEYCLOAK_PROVIDER_A_CLIENT_SECRET` | `wagering:write`, `wagering:read` | `provider-a` |
| `provider-b` | `KEYCLOAK_PROVIDER_B_CLIENT_SECRET` | `wagering:write`, `wagering:read` | `provider-b` |

## Roteiro: da carteira ao ledger

Carregue as variáveis e defina uma função que pede um token:

```sh
set -a; . ./.env; set +a

token() {
  curl -s -X POST http://localhost:8090/realms/wallet/protocol/openid-connect/token \
    -d grant_type=client_credentials -d client_id="$1" -d client_secret="$2" \
    | python3 -c 'import sys, json; print(json.load(sys.stdin)["access_token"])'
}

INTERNAL=$(token wallet-internal "$KEYCLOAK_INTERNAL_CLIENT_SECRET")
PROVIDER_A=$(token provider-a "$KEYCLOAK_PROVIDER_A_CLIENT_SECRET")
PROVIDER_B=$(token provider-b "$KEYCLOAK_PROVIDER_B_CLIENT_SECRET")
```

Um token vale 5 minutos. O emissor dos tokens é sempre `http://keycloak:8080/realms/wallet`, mesmo quando pedidos por `localhost:8090`.

**1. Abrir a carteira** (serviço interno, instância 1):

```sh
PLAYER=$(python3 -c 'import uuid; print(uuid.uuid4())')

curl -s -X POST http://localhost:8081/wallets \
  -H "Authorization: Bearer $INTERNAL" \
  -d "{\"playerId\":\"$PLAYER\",\"initialBalance\":{\"amount\":\"1000.00\",\"currency\":\"BRL\"}}"
```

Guarde o `id` da resposta:

```sh
WALLET=<id devolvido>
```

**2. Enviar uma aposta** (provedor A, instância 2):

```sh
curl -s -X POST http://localhost:8082/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_A" \
  -H "Idempotency-Key: provider-a:transaction-123" \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"transaction-123\",\"playerId\":\"$PLAYER\",\"walletId\":\"$WALLET\",\"roundId\":\"round-987\",\"gameId\":\"fortune-chimp\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}"
```

Resposta: `{"transactionId":"...","status":"PROCESSED","balance":{"amount":"975.00","currency":"BRL"},"idempotentReplay":false}`. Repita o mesmo comando em qualquer instância: a resposta é a mesma, com `idempotentReplay` verdadeiro, e o saldo não muda.

**3. Consultar o ledger e reconciliar** (serviço interno, instância 3):

```sh
curl -s "http://localhost:8083/wallets/$WALLET/ledger?limit=50" -H "Authorization: Bearer $INTERNAL"
curl -s -X POST "http://localhost:8083/wallets/$WALLET/reconciliation" -H "Authorization: Bearer $INTERNAL"
```

**4. Outras chamadas**

```sh
# carteira
curl -s "http://localhost:8081/wallets/$WALLET" -H "Authorization: Bearer $INTERNAL"

# transação pelo identificador externo do provedor
curl -s http://localhost:8081/providers/provider-a/wagering/transactions/transaction-123 -H "Authorization: Bearer $PROVIDER_A"

# reembolso da aposta: acrescenta a referência
curl -s -X POST http://localhost:8081/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_A" -H "Idempotency-Key: provider-a:transaction-124" \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"transaction-124\",\"playerId\":\"$PLAYER\",\"walletId\":\"$WALLET\",\"roundId\":\"round-987\",\"gameId\":\"fortune-chimp\",\"kind\":\"REFUND\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"},\"referenceExternalTransactionId\":\"transaction-123\"}"

# acessos negados
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8081/wallets/$WALLET                                    # 401: sem token
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8081/wallets/$WALLET -H "Authorization: Bearer $PROVIDER_A"   # 403: provedor não lê carteira
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8081/providers/provider-a/wagering/transactions/transaction-123 -H "Authorization: Bearer $PROVIDER_B"   # 403: outro provedor

# saúde e métricas (públicas)
curl -s http://localhost:8081/health/live
curl -s http://localhost:8081/health/ready
curl -s http://localhost:8081/metrics | grep wager_transactions_total
```

Os códigos de status e os corpos de erro estão na seção 11 do `ARCHITECTURE.md`.

## Enviar uma operação pela fila

O consumidor lê `wager-transactions.fifo`. Com a AWS CLI instalada (as credenciais são as do `.env`):

```sh
aws --endpoint-url http://localhost:4566 --region "$AWS_REGION" sqs send-message \
  --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
  --message-group-id "$WALLET" --message-deduplication-id msg-123 \
  --message-body "{\"messageId\":\"msg-123\",\"type\":\"WagerTransactionRequested\",\"occurredAt\":\"2026-09-08T12:00:00.000Z\",\"data\":{\"providerId\":\"provider-a\",\"externalTransactionId\":\"transaction-200\",\"idempotencyKey\":\"provider-a:transaction-200\",\"playerId\":\"$PLAYER\",\"walletId\":\"$WALLET\",\"roundId\":\"round-987\",\"gameId\":\"fortune-chimp\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}}"
```

Sem a AWS CLI no host, a imagem oficial `amazon/aws-cli` aceita os mesmos argumentos (`docker run --rm --network host -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY amazon/aws-cli:<versão> ...`); escolha e fixe a versão.

Para ver o resultado: consulte a transação por `transaction-200`, como no passo 4. Os eventos publicados podem ser lidos de `wallet-events.fifo` com `aws sqs receive-message`.

## Testes

| Nível | Comando | Precisa de Docker | O que sobe |
| --- | --- | --- | --- |
| unitário | `go test ./...` ou `make test` | não | nada |
| unitário com detector de corrida | `go test -race ./...` ou `make race` | não | nada |
| análise estática | `go vet ./...` | não | |
| formatação | `gofmt -l .` (não deve listar nada) | não | |
| integração | `make integration` (`go test -race -p 1 -tags=integration ./...`) | sim | cada pacote sobe os próprios containers de PostgreSQL, Keycloak e MiniStack com `testcontainers-go` |
| ponta a ponta, três instâncias | `make e2e` | sim | a stack do Compose, com o prazo de referência pendente reduzido para 20 s |
| dinheiro sem ponto flutuante | `make nofloat` | não | procura `float32`, `float64`, `ParseFloat` e `big.Float` no domínio e na aplicação |

Notas:

- Os testes de integração e os ponta a ponta ficam atrás das build tags `integration` e `e2e`. Sem elas, `go test ./...` não precisa de Docker.
- `make integration` leva alguns minutos: o Keycloak demora cerca de 30 segundos para subir em cada pacote que o usa. Os pacotes rodam um por vez (`-p 1`), para não subir vários Keycloaks ao mesmo tempo; se a máquina tiver pouca memória, derrube a stack do Compose antes (`make down`).
- `make e2e` deixa a stack de pé com o prazo de 20 s; para voltar ao padrão, rode `make up` de novo.
- `make e2e` sobe a stack (`docker compose up --build --wait`) e roda `go test -tags=e2e ./test/e2e/...`. Ele usa as portas `5432`, `4566`, `8081` a `8083` e `8090`, e lê os segredos do `.env`.

## Múltiplas instâncias e simulações de falha

Os testes de `test/e2e` já fazem estas simulações contra a stack; para repetir à mão:

| Simulação | Comando | O que observar |
| --- | --- | --- |
| matar uma instância | `docker compose kill wallet-2` e depois `docker compose up -d wallet-2` | as outras duas continuam atendendo; reenvios devolvem o resultado original; eventos pendentes são publicados por outra instância |
| reiniciar todas | `docker compose restart wallet-1 wallet-2 wallet-3` | replays continuam devolvendo o resultado original; referências pendentes são retomadas |
| banco fora do ar | `docker compose stop postgres` e depois `docker compose start postgres` | `503` com `Retry-After` nas operações e em `/health/ready`; `/health/live` segue `200`; depois da volta o reenvio é processado uma única vez |
| SQS fora do ar | `docker compose stop ministack`, depois `docker compose start ministack` e `docker compose run --rm queues` | as operações por HTTP continuam; os eventos ficam pendentes na outbox e saem depois da volta |
| reversão antes da referência | enviar um `REFUND` com `referenceExternalTransactionId` de uma aposta que ainda não existe | resposta `202` com `PENDING_REFERENCE`; ao enviar a aposta, o reembolso é processado em seguida; sem a aposta, vira `REJECTED` com `REFERENCE_NOT_FOUND` ao fim do prazo |

Para acompanhar: `docker compose logs -f wallet-1 wallet-2 wallet-3` (logs em JSON) e `curl -s http://localhost:8081/metrics`.

## Estrutura

```
cmd/server/                     composição com Fx
cmd/queues/                     criação das filas
internal/domain/                money, ids, failure, ledger, wallet, wager, events
internal/app/                   casos de uso, portas e hash do conteúdo
internal/infra/postgres/        pool, transações, repositórios, outbox e inbox
internal/infra/sqs/             filas, consumidor e publicador
internal/infra/httpapi/         rotas, middlewares, DTOs
internal/infra/auth/            validação de tokens OIDC
internal/infra/observability/   logs, métricas, saúde
internal/worker/                laço dos workers e publicador da outbox
internal/bootstrap/             configuração e módulos Fx
migrations/                     NNNN_nome.up.sql e NNNN_nome.down.sql
deploy/keycloak/                realm importado pelo Keycloak
test/integration/               testes entre camadas, com containers reais
test/e2e/                       testes contra as três instâncias
```
