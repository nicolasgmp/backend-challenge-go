package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/infra/observability"
	"jungle-gaming-challeng/internal/infra/observability/logtest"
)

const secretValue = "s3cr3t-value"

func TestSecretIsRedactedEverywhere(t *testing.T) {
	secret := observability.NewSecret(secretValue)
	logs := &logtest.Buffer{}
	observability.NewLogger(logs, slog.LevelInfo).Info("configuration loaded", "password", secret, "nested", struct{ Password observability.Secret }{secret})
	encoded, err := json.Marshal(map[string]any{"password": secret, "pointer": &secret})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	forms := map[string]string{
		"String": secret.String(),
		"%v":     fmt.Sprintf("%v", secret),
		"%+v":    fmt.Sprintf("%+v", struct{ Password observability.Secret }{secret}),
		"%#v":    fmt.Sprintf("%#v", secret),
		"%s":     fmt.Sprintf("%s", &secret),
		"error":  fmt.Errorf("connect with %v: %w", secret, errors.New("refused")).Error(),
		"json":   string(encoded),
		"slog":   logs.String(),
	}
	for name, text := range forms {
		if strings.Contains(text, secretValue) || !strings.Contains(text, "[REDACTED]") {
			t.Errorf("%s = %q, want the value hidden behind [REDACTED]", name, text)
		}
	}

	if secret.Reveal() != secretValue {
		t.Fatal("Reveal does not return the stored value")
	}
}

func TestLoggerWritesJSONInUTCWithContextIdentifiers(t *testing.T) {
	logs := &logtest.Buffer{}
	logger := observability.NewLogger(logs, slog.LevelInfo)
	ctx := observability.WithCorrelationID(context.Background(), "correlation-1")
	ctx = observability.WithMessageID(ctx, "message-1")

	logger.InfoContext(ctx, "wager transaction handled",
		"transactionId", "transaction-1", "walletId", "wallet-1", "providerId", "provider-a")
	logger.DebugContext(ctx, "below the level")
	logger.With("component", "consumer").WarnContext(context.Background(), "no identifiers")
	logger.With("component", "consumer").InfoContext(ctx, "component and identifiers")

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[2], `"component":"consumer"`) || !strings.Contains(lines[2], `"correlationId":"correlation-1"`) {
		t.Fatalf("lines = %d, want 3 (the debug line is below the level), the last with the component and the context identifiers:\n%s", len(lines), logs.String())
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("the line is not JSON: %v", err)
	}
	want := map[string]string{
		"level": "INFO", "msg": "wager transaction handled",
		"correlationId": "correlation-1", "messageId": "message-1",
		"transactionId": "transaction-1", "walletId": "wallet-1", "providerId": "provider-a",
	}
	for key, value := range want {
		if first[key] != value {
			t.Errorf("%s = %v, want %q", key, first[key], value)
		}
	}
	stamp, err := time.Parse(time.RFC3339Nano, fmt.Sprint(first["time"]))
	if err != nil || !strings.HasSuffix(fmt.Sprint(first["time"]), "Z") || time.Since(stamp) > time.Minute {
		t.Fatalf("time = %v, want the current instant in UTC", first["time"])
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("the line is not JSON: %v", err)
	}
	if second["component"] != "consumer" || second["correlationId"] != nil {
		t.Fatalf("second line = %v, want the component and no identifier from another context", second)
	}
}

func TestContextIdentifiersDoNotLeakBetweenBranches(t *testing.T) {
	base := observability.WithCorrelationID(context.Background(), "correlation-1")
	base = observability.WithLogAttrs(base, slog.String("consumer", "wager"))
	base = observability.WithLogAttrs(base, slog.String("queue", "inbound"))
	first := observability.WithMessageID(base, "message-1")
	second := observability.WithMessageID(base, "message-2")

	logs := &logtest.Buffer{}
	logger := observability.NewLogger(logs, slog.LevelInfo)
	logger.InfoContext(first, "first")
	logger.InfoContext(second, "second")

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if !strings.Contains(lines[0], "message-1") || strings.Contains(lines[0], "message-2") || !strings.Contains(lines[1], "message-2") {
		t.Fatalf("lines =\n%s\nwant each with its own message id", logs.String())
	}
}

func TestMetricsAreExposed(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.TransactionResult("http", "BET", "REJECTED", "INSUFFICIENT_FUNDS")
	metrics.TransactionResult("http", "BET", "REJECTED", "INSUFFICIENT_FUNDS")
	metrics.ProcessingDuration("sqs", 20*time.Millisecond)
	metrics.Duplicate("replay")
	metrics.Duplicate("inbox")
	metrics.Retry("outbox")
	metrics.DeadLetter("invalid_message")
	metrics.ConcurrencyConflict("lock_timeout")
	metrics.OutboxLag(9 * time.Second)
	metrics.OutboxLag(3 * time.Second)
	metrics.ReconciliationDivergence()

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()

	for _, want := range []string{
		`wager_transactions_total{channel="http",failure_code="INSUFFICIENT_FUNDS",kind="BET",status="REJECTED"} 2`,
		`processing_duration_seconds_count{channel="sqs"} 1`,
		`duplicates_total{source="replay"} 1`,
		`duplicates_total{source="inbox"} 1`,
		`retries_total{component="outbox"} 1`,
		`dlq_messages_total{reason="invalid_message"} 1`,
		`concurrency_conflicts_total{type="lock_timeout"} 1`,
		`outbox_lag_seconds 3`,
		`reconciliation_divergences_total 1`,
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("/metrics does not contain the line %q", want)
		}
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestHealth(t *testing.T) {
	up := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("dial tcp 10.0.0.1:5432: refused") }

	tests := []struct {
		name     string
		checks   []observability.Check
		shutdown bool
		want     []string
	}{
		{"all ready", []observability.Check{{Name: "postgres", Ping: up}, {Name: "sqs", Ping: up}}, false, nil},
		{"one failing", []observability.Check{{Name: "postgres", Ping: up}, {Name: "sqs", Ping: down}}, false, []string{"sqs"}},
		{"both failing", []observability.Check{{Name: "postgres", Ping: down}, {Name: "sqs", Ping: down}}, false, []string{"postgres", "sqs"}},
		{"shutting down", []observability.Check{{Name: "postgres", Ping: up}}, true, []string{observability.ShuttingDown}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := observability.NewHealth(tt.checks...)
			if tt.shutdown {
				health.BeginShutdown()
			}
			if got := health.NotReady(context.Background()); !slices.Equal(got, tt.want) {
				t.Fatalf("NotReady = %v, want %v", got, tt.want)
			}
		})
	}
}

type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Errorf(string, ...any) { r.failed = true }

func TestLogLeakHelper(t *testing.T) {
	logs := `{"msg":"handled","walletId":"wallet-1","amount":"25.00"}`

	if found := logtest.Leaked(logs, "25.00", "token-abc", ""); !slices.Equal(found, []string{"25.00"}) {
		t.Fatalf("Leaked = %v, want only the value present in the logs", found)
	}

	leaking := &recorder{TB: t}
	logtest.AssertNoLeak(leaking, logs, "25.00")
	clean := &recorder{TB: t}
	logtest.AssertNoLeak(clean, logs, "975.00", "token-abc")
	if !leaking.failed || clean.failed {
		t.Fatalf("AssertNoLeak failed = %t for leaking logs and %t for clean logs, want true and false", leaking.failed, clean.failed)
	}
}
