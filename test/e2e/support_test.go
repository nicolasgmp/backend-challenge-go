//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/infra/sqs"
)

const (
	repoRoot    = "../.."
	keycloakURL = "http://localhost:8090"
	brokerURL   = "http://localhost:4566"
)

var instances = []string{"http://localhost:8081", "http://localhost:8082", "http://localhost:8083"}

type stack struct {
	env      map[string]string
	client   *http.Client
	broker   *awssqs.Client
	queues   sqs.Queues
	db       *pgxpool.Pool
	internal string
	tokens   map[string]string
	next     atomic.Int64
}

var (
	shared     *stack
	sharedOnce sync.Once
)

func environment(t *testing.T) *stack {
	t.Helper()

	sharedOnce.Do(func() { shared = connect(t) })
	if shared == nil {
		t.Fatal("the e2e environment could not be prepared; is the compose stack up (make up)?")
	}
	return shared
}

func connect(t *testing.T) *stack {
	t.Helper()

	env := readEnvFile(t)
	s := &stack{env: env, client: &http.Client{Timeout: 30 * time.Second}, tokens: map[string]string{}}
	s.internal = s.token(t, "wallet-internal", env["KEYCLOAK_INTERNAL_CLIENT_SECRET"])
	s.tokens["provider-a"] = s.token(t, "provider-a", env["KEYCLOAK_PROVIDER_A_CLIENT_SECRET"])
	s.tokens["provider-b"] = s.token(t, "provider-b", env["KEYCLOAK_PROVIDER_B_CLIENT_SECRET"])

	broker := sqs.NewClient(sqs.Config{
		Endpoint: brokerURL, Region: env["AWS_REGION"],
		AccessKeyID: env["AWS_ACCESS_KEY_ID"], SecretAccessKey: env["AWS_SECRET_ACCESS_KEY"],
	})
	s.broker = broker
	var err error
	if s.queues, err = sqs.FindQueues(context.Background(), broker); err != nil {
		t.Fatalf("find queues: %v", err)
	}

	databaseURL := "postgres://wallet:" + url.QueryEscape(env["POSTGRES_PASSWORD"]) + "@localhost:5432/wallet?sslmode=disable"
	if s.db, err = pgxpool.New(context.Background(), databaseURL); err != nil {
		t.Fatalf("database pool: %v", err)
	}
	return s
}

func readEnvFile(t *testing.T) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(repoRoot + "/.env")
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	env := map[string]string{}
	for line := range strings.Lines(string(raw)) {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && !strings.HasPrefix(name, "#") {
			env[name] = value
		}
	}
	return env
}

func (s *stack) token(t *testing.T, clientID, secret string) string {
	t.Helper()

	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}}
	response, err := s.client.PostForm(keycloakURL+"/realms/wallet/protocol/openid-connect/token", form)
	if err != nil {
		t.Fatalf("token for %s: %v", clientID, err)
	}
	defer response.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.AccessToken == "" {
		t.Fatalf("token for %s: status %d without a token", clientID, response.StatusCode)
	}
	return body.AccessToken
}

type answer struct {
	status int
	raw    string
	body   map[string]any
	err    error
}

func (a answer) text(key string) string {
	value, _ := a.body[key].(string)
	return value
}

func (a answer) amount(key string) string {
	money, _ := a.body[key].(map[string]any)
	value, _ := money["amount"].(string)
	return value
}

func (s *stack) call(instance int, method, path, token, key string, body any) answer {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return answer{err: err}
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, instances[instance%len(instances)]+path, payload)
	if err != nil {
		return answer{err: err}
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return answer{err: err}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	result := answer{status: response.StatusCode, raw: string(raw), err: err}
	_ = json.Unmarshal(raw, &result.body)
	return result
}

type wallet struct {
	id     string
	player string
}

func (s *stack) open(t *testing.T, balance string) wallet {
	t.Helper()

	player := uuid.NewString()
	opened := s.call(0, http.MethodPost, "/wallets", s.internal, "", map[string]any{
		"playerId": player, "initialBalance": map[string]string{"amount": balance, "currency": "BRL"},
	})
	if opened.err != nil || opened.status != http.StatusCreated {
		t.Fatalf("open wallet = %d %s (%v), want 201", opened.status, opened.raw, opened.err)
	}
	return wallet{id: opened.text("id"), player: player}
}

func (s *stack) externalID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), s.next.Add(1))
}

func operation(w wallet, provider, kind, value, externalID, reference string) map[string]any {
	body := map[string]any{
		"providerId": provider, "externalTransactionId": externalID, "playerId": w.player, "walletId": w.id,
		"roundId": "round-1", "gameId": "fortune-chimp", "kind": kind,
		"money": map[string]string{"amount": value, "currency": "BRL"},
	}
	if reference != "" {
		body["referenceExternalTransactionId"] = reference
	}
	return body
}

func (s *stack) submit(instance int, w wallet, kind, value, externalID, reference string) answer {
	return s.call(instance, http.MethodPost, "/wagering/transactions", s.tokens["provider-a"], "provider-a:"+externalID,
		operation(w, "provider-a", kind, value, externalID, reference))
}

func (s *stack) sendMessage(t *testing.T, w wallet, messageID, kind, value, externalID, reference string) {
	t.Helper()

	data := operation(w, "provider-a", kind, value, externalID, reference)
	data["idempotencyKey"] = "provider-a:" + externalID
	body, err := json.Marshal(map[string]any{
		"messageId": messageID, "type": "WagerTransactionRequested", "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "data": data,
	})
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	_, err = s.broker.SendMessage(context.Background(), &awssqs.SendMessageInput{
		QueueUrl: aws.String(s.queues.InboundURL), MessageBody: aws.String(string(body)),
		MessageGroupId: aws.String(w.id), MessageDeduplicationId: aws.String(messageID),
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
}

func (s *stack) wantWallet(t *testing.T, w wallet, balance string, entries int) {
	t.Helper()

	reconciliation := s.call(1, http.MethodPost, "/wallets/"+w.id+"/reconciliation", s.internal, "", nil)
	if reconciliation.err != nil || reconciliation.status != http.StatusOK {
		t.Fatalf("reconciliation = %d %s (%v)", reconciliation.status, reconciliation.raw, reconciliation.err)
	}
	if reconciliation.body["consistent"] != true {
		t.Fatalf("wallet %s diverges from its ledger: %s", w.id, reconciliation.raw)
	}
	if got := reconciliation.amount("storedBalance"); got != balance {
		t.Fatalf("balance = %s, want %s", got, balance)
	}
	if got := reconciliation.body["checkedEntries"]; got != float64(entries) {
		t.Fatalf("ledger entries = %v, want %d", got, entries)
	}
}

func (s *stack) transaction(t *testing.T, externalID string) answer {
	t.Helper()

	return s.call(2, http.MethodGet, "/providers/provider-a/wagering/transactions/"+externalID, s.tokens["provider-a"], "", nil)
}

func (s *stack) waitStatus(t *testing.T, externalID, status string, limit time.Duration) answer {
	t.Helper()

	deadline := time.Now().Add(limit)
	var last answer
	for time.Now().Before(deadline) {
		if last = s.transaction(t, externalID); last.text("status") == status {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("transaction %s = %d %s after %s, want status %s", externalID, last.status, last.raw, limit, status)
	return last
}

func (s *stack) count(t *testing.T, query string, args ...any) int {
	t.Helper()

	var count int
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func (s *stack) waitPublished(t *testing.T, w wallet, limit time.Duration) {
	t.Helper()

	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if s.count(t, `SELECT count(*) FROM outbox WHERE group_key = $1 AND published_at IS NULL`, w.id) == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("wallet %s still has unpublished events after %s", w.id, limit)
}

func (s *stack) drainEvents(t *testing.T) map[string]int {
	t.Helper()

	seen := map[string]int{}
	for empty := 0; empty < 2; {
		output, err := s.broker.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
			QueueUrl: aws.String(s.queues.EventsURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
		})
		if err != nil {
			t.Fatalf("receive events: %v", err)
		}
		if len(output.Messages) == 0 {
			empty++
			continue
		}
		empty = 0
		for _, message := range output.Messages {
			var event struct {
				EventID string `json:"eventId"`
			}
			if err := json.Unmarshal([]byte(aws.ToString(message.Body)), &event); err != nil || event.EventID == "" {
				t.Fatalf("event body is not an envelope: %s", aws.ToString(message.Body))
			}
			seen[event.EventID]++
			_, _ = s.broker.DeleteMessage(context.Background(), &awssqs.DeleteMessageInput{
				QueueUrl: aws.String(s.queues.EventsURL), ReceiptHandle: message.ReceiptHandle,
			})
		}
	}
	return seen
}

func compose(t *testing.T, args ...string) {
	t.Helper()

	if err := composeErr(args...); err != nil {
		t.Fatal(err)
	}
}

func composeErr(args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose %s: %w\n%s", strings.Join(args, " "), err, output)
	}
	return nil
}

func (s *stack) waitHandled(t *testing.T, messageID string, limit time.Duration) {
	t.Helper()

	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if s.count(t, `SELECT count(*) FROM inbox WHERE message_id = $1 AND completed_at IS NOT NULL`, messageID) == 1 {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("message %s was not received and handled by the consumer within %s", messageID, limit)
}

func (s *stack) waitReady(t *testing.T, instance int, want int, limit time.Duration) {
	t.Helper()

	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		response, err := s.client.Get(instances[instance] + "/health/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("instance %d did not answer %d on /health/ready within %s", instance+1, want, limit)
}

func inParallel(n int, fn func(i int) answer) []answer {
	answers := make([]answer, n)
	var group sync.WaitGroup
	for i := range n {
		group.Go(func() { answers[i] = fn(i) })
	}
	group.Wait()
	return answers
}
