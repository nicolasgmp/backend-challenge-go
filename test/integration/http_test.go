//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/infra/auth"
	"jungle-gaming-challeng/internal/infra/auth/kctest"
	"jungle-gaming-challeng/internal/infra/httpapi"
	"jungle-gaming-challeng/internal/infra/observability"
	"jungle-gaming-challeng/internal/infra/observability/logtest"
)

const playerUUID = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"

type httpStack struct {
	environment
	url  string
	logs *logtest.Buffer
}

func newHTTPStack(t *testing.T, keycloak *kctest.Server) httpStack {
	t.Helper()

	e := newEnvironment(t)
	logs := &logtest.Buffer{}
	logger := observability.NewLogger(logs, slog.LevelDebug)
	metrics := observability.NewMetrics()
	e.service.Logger = logger
	e.service.Metrics = metrics

	api := httpapi.New(httpapi.Dependencies{
		Service: e.service,
		Verifier: auth.NewVerifier(context.Background(), auth.Config{
			IssuerURL: keycloak.IssuerURL(), KeysURL: keycloak.KeysURL(), Audience: kctest.Audience,
		}),
		Readiness:      observability.NewHealth(),
		Metrics:        metrics.Handler(),
		Logger:         logger,
		RequestTimeout: 10 * time.Second,
		MaxBodyBytes:   1 << 16,
	})
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return httpStack{environment: e, url: server.URL, logs: logs}
}

type response struct {
	status int
	raw    string
	body   map[string]any
}

func (s httpStack) call(t *testing.T, method, path, token, key, body string) response {
	t.Helper()

	request, err := http.NewRequest(method, s.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	answer, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer answer.Body.Close()

	raw, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	result := response{status: answer.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &result.body)
	return result
}

func betBody(provider, walletID string) string {
	return `{"providerId":"` + provider + `","externalTransactionId":"transaction-123","playerId":"` + playerUUID +
		`","walletId":"` + walletID + `","roundId":"round-987","gameId":"fortune-chimp","kind":"BET",` +
		`"money":{"amount":"25.00","currency":"BRL"}}`
}

func withoutCorrelation(r response) string {
	delete(r.body, "correlationId")
	encoded, _ := json.Marshal(r.body)
	return string(encoded)
}

func TestHTTPAccessControlWithKeycloak(t *testing.T) {
	ctx := context.Background()
	keycloak := kctest.Start(ctx, t, "../../deploy/keycloak/wallet-realm.json")
	stack := newHTTPStack(t, keycloak)

	providerA := keycloak.Token(ctx, t, kctest.ProviderAClient, kctest.ProviderASecret)
	providerB := keycloak.Token(ctx, t, kctest.ProviderBClient, kctest.ProviderBSecret)
	internal := keycloak.Token(ctx, t, kctest.InternalClient, kctest.InternalSecret)
	const key = "provider-a:transaction-123"
	const openBody = `{"playerId":"` + playerUUID + `","initialBalance":{"amount":"1000.00","currency":"BRL"}}`

	denied := func(name string, got response, status int, code string) {
		t.Helper()
		if got.status != status || got.body["code"] != code {
			t.Fatalf("%s: response = %d %s, want %d %s", name, got.status, got.raw, status, code)
		}
		for _, leaked := range []string{"1000.00", "975.00", "25.00", playerUUID, "transaction-123"} {
			if strings.Contains(got.raw, leaked) {
				t.Fatalf("%s: the error body exposes %q: %s", name, leaked, got.raw)
			}
		}
	}

	denied("provider opening a wallet", stack.call(t, http.MethodPost, "/wallets", providerA, "", openBody), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	denied("opening a wallet without a token", stack.call(t, http.MethodPost, "/wallets", "", "", openBody), http.StatusUnauthorized, "UNAUTHORIZED")
	denied("opening a wallet with a forged token", stack.call(t, http.MethodPost, "/wallets", providerA+"x", "", openBody), http.StatusUnauthorized, "UNAUTHORIZED")
	if got := stack.counts(t); got != (counts{}) {
		t.Fatalf("rows after denied openings = %+v, want none", got)
	}

	opened := stack.call(t, http.MethodPost, "/wallets", internal, "", openBody)
	walletID, _ := opened.body["id"].(string)
	if opened.status != http.StatusCreated || walletID == "" {
		t.Fatalf("opening by the internal service = %d %s, want 201", opened.status, opened.raw)
	}
	afterOpening := stack.counts(t)

	denied("internal service sending a bet", stack.call(t, http.MethodPost, "/wagering/transactions", internal, key, betBody("provider-a", walletID)), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	denied("provider B sending as provider A", stack.call(t, http.MethodPost, "/wagering/transactions", providerB, key, betBody("provider-a", walletID)), http.StatusForbidden, "PROVIDER_MISMATCH")
	denied("bet without a token", stack.call(t, http.MethodPost, "/wagering/transactions", "", key, betBody("provider-a", walletID)), http.StatusUnauthorized, "UNAUTHORIZED")
	denied("provider reading a wallet", stack.call(t, http.MethodGet, "/wallets/"+walletID, providerA, "", ""), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	denied("provider reading a ledger", stack.call(t, http.MethodGet, "/wallets/"+walletID+"/ledger", providerA, "", ""), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	denied("provider reconciling", stack.call(t, http.MethodPost, "/wallets/"+walletID+"/reconciliation", providerA, "", ""), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	if got := stack.counts(t); got != afterOpening {
		t.Fatalf("rows after denied calls = %+v, want them unchanged: %+v", got, afterOpening)
	}

	betOfA := stack.call(t, http.MethodPost, "/wagering/transactions", providerA, key, betBody("provider-a", walletID))
	transactionOfA, _ := betOfA.body["transactionId"].(string)
	if betOfA.status != http.StatusOK || betOfA.body["status"] != "PROCESSED" || betOfA.body["idempotentReplay"] != false {
		t.Fatalf("bet of provider A = %d %s, want 200 PROCESSED", betOfA.status, betOfA.raw)
	}

	betOfB := stack.call(t, http.MethodPost, "/wagering/transactions", providerB, key, betBody("provider-b", walletID))
	if betOfB.status != http.StatusOK || betOfB.body["idempotentReplay"] != false || betOfB.body["transactionId"] == transactionOfA {
		t.Fatalf("same key and external id from provider B = %d %s, want a new operation, not a replay of A", betOfB.status, betOfB.raw)
	}

	replay := stack.call(t, http.MethodPost, "/wagering/transactions", providerA, key, betBody("provider-a", walletID))
	if replay.status != http.StatusOK || replay.body["idempotentReplay"] != true || replay.body["transactionId"] != transactionOfA {
		t.Fatalf("replay by provider A = %d %s, want its own original result", replay.status, replay.raw)
	}
	if balance, _ := replay.body["balance"].(map[string]any); balance["amount"] != "975.00" {
		t.Fatalf("replay balance = %v, want the 975.00 seen by provider A originally", replay.body["balance"])
	}

	if got := stack.call(t, http.MethodGet, "/wagering/transactions/"+transactionOfA, providerA, "", ""); got.status != http.StatusOK {
		t.Fatalf("owner reading its transaction = %d %s, want 200", got.status, got.raw)
	}
	if got := stack.call(t, http.MethodGet, "/wagering/transactions/"+transactionOfA, internal, "", ""); got.status != http.StatusOK {
		t.Fatalf("internal service reading a transaction = %d %s, want 200", got.status, got.raw)
	}
	ofAnother := stack.call(t, http.MethodGet, "/wagering/transactions/"+transactionOfA, providerB, "", "")
	unknown := stack.call(t, http.MethodGet, "/wagering/transactions/"+walletID, providerB, "", "")
	denied("provider B reading a transaction of A", ofAnother, http.StatusNotFound, "TRANSACTION_NOT_FOUND")
	if withoutCorrelation(ofAnother) != withoutCorrelation(unknown) {
		t.Fatalf("bodies differ: %s and %s, want the same answer as for an unknown id", ofAnother.raw, unknown.raw)
	}
	denied("provider B on the path of provider A",
		stack.call(t, http.MethodGet, "/providers/provider-a/wagering/transactions/transaction-123", providerB, "", ""),
		http.StatusForbidden, "PROVIDER_MISMATCH")
	if got := stack.call(t, http.MethodGet, "/providers/provider-a/wagering/transactions/transaction-123", providerA, "", ""); got.status != http.StatusOK || got.body["transactionId"] != transactionOfA {
		t.Fatalf("owner reading by external id = %d %s, want 200 with its transaction", got.status, got.raw)
	}

	stack.wantWallet(t, parsed(t, ids.ParseWalletID, walletID), "950.00", 3)

	logs := stack.logs.String()
	if !strings.Contains(logs, transactionOfA) || !strings.Contains(logs, walletID) || !strings.Contains(logs, "provider-a") || !strings.Contains(logs, "correlationId") {
		t.Fatalf("logs lack the tracing identifiers:\n%s", logs)
	}
	logtest.AssertNoLeak(t, logs, providerA, providerB, internal, "1000.00", "975.00", "950.00", "25.00",
		kctest.ProviderASecret, kctest.InternalSecret)
}
