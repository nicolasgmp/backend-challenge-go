# Arquitetura

Este documento registra as decisões técnicas da solução. Cada seção é preenchida no grupo de tasks de `openspec/changes/wallet-wagering-service/tasks.md` em que a decisão é tomada; seções ainda vazias estão marcadas como pendentes.

## 1. Dinheiro

Implementado em `internal/domain/money`. O enunciado trata do assunto na seção 6.1.

### 1.1. Representação

`Money` guarda um `int64` com a quantidade de unidades mínimas (centavos) e uma `Currency`. A escala é fixa em duas casas: `"25.00"` vira `2500` e `"0.05"` vira `5`. Os dois campos são privados e todo método devolve um valor novo, então um `Money` nunca muda depois de criado.

Nenhuma etapa usa ponto flutuante. O texto de entrada é convertido em inteiro dígito a dígito, e a saída é montada com divisão e resto por 100. `make nofloat` falha se `float32` ou `float64` aparecer em `internal/domain` ou `internal/app`.

- **Por quê:** todo valor aceito é um inteiro exato, e soma, subtração, negação e comparação viram operações de inteiros.
- **Alternativa descartada:** uma biblioteca decimal. O serviço não multiplica, não divide e não arredonda, e a validação de formato da seção 1.3 teria de ser escrita de qualquer modo, porque os parsers dessas bibliotecas aceitam formas como `"25"` e `"1e2"`.
- **Base:** "Use `int64` em unidades mínimas ou uma biblioteca decimal de precisão exata." A seção 14 torna eliminatório o "cálculo monetário em ponto flutuante".

### 1.2. Limites e overflow

| Limite | Unidades mínimas | Forma decimal |
| --- | --- | --- |
| Maior valor | `9223372036854775807` | `92233720368547758.07` |
| Menor valor | `-9223372036854775808` | `-92233720368547758.08` |

`Parse`, `Add`, `Sub` e `Neg` conferem o limite antes de fazer a conta e devolvem `ErrOverflow`. A checagem vem antes porque a aritmética de `int64` em Go dá a volta em silêncio. `Neg` do menor valor é overflow, pois o oposto dele não cabe em `int64`.

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
| `ErrInvalidAmount` | texto fora do formato |
| `ErrNegativeAmount` | valor negativo em entrada externa |
| `ErrInvalidCurrency` | moeda fora da lista |
| `ErrCurrencyMismatch` | soma, subtração ou comparação entre moedas diferentes |
| `ErrOverflow` | resultado fora dos limites de `int64` |
| `ErrUninitialized` | operação sobre `Money{}` ou `Currency{}` |

`Money{}` é inválido. `Add`, `Sub`, `Neg`, `Cmp` e `MarshalJSON` devolvem `ErrUninitialized`. `Equal`, `IsZero`, `IsPositive` e `IsNegative` devolvem `false`. As leituras `MinorUnits`, `Currency` e `Amount` não devolvem erro. Essa divisão entre operações que falham e leituras que não falham é interpretação deste projeto.

- **Base:** "Aritmética e comparação de valores monetários exigem moedas compatíveis." e, na seção 6, "Erros de domínio devem ser classificáveis por tipo ou `errors.Is`/`errors.As`."

### 1.6. JSON

A escrita produz `{"amount":"25.00","currency":"BRL"}`, com `amount` sempre em string. Na leitura, `amount` precisa ser string, os dois campos são obrigatórios e um campo desconhecido é recusado. Um `amount` numérico é recusado na decodificação, sem ser convertido em número. A leitura usa `ParseCurrency` e `Parse`, as mesmas funções das demais entradas.

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

_Pendente (grupo 3 do `tasks.md`)._

## 14. Observabilidade

_Pendente (grupo 12 do `tasks.md`)._

## 15. Limitações, interpretações adotadas e trabalho não concluído

_Pendente (grupo 19 do `tasks.md`)._