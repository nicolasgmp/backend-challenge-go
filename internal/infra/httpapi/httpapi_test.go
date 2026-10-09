package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
	"jungle-gaming-challeng/internal/infra/httpapi"
	"jungle-gaming-challeng/internal/infra/observability"
	"jungle-gaming-challeng/internal/infra/observability/logtest"
)

const (
	walletUUID      = "0192f291-27dd-7d3f-8071-5f8685deef37"
	playerUUID      = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"
	transactionUUID = "0192f298-345e-7e38-af88-e43f851a819d"

	providerAToken = "token-of-provider-a"
	providerBToken = "token-of-provider-b"
	internalToken  = "token-of-the-internal-service"

	betBody = `{"providerId":"provider-a","externalTransactionId":"transaction-123",` +
		`"playerId":"` + playerUUID + `","walletId":"` + walletUUID + `","roundId":"round-987",` +
		`"gameId":"fortune-chimp","kind":"BET","money":{"amount":"25.00","currency":"BRL"}}`
)

var createdAt = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type fakeService struct {
	calls                  []string
	openWallet             func(app.OpenWalletInput) (wallet.State, error)
	getWallet              func(ids.WalletID) (wallet.State, error)
	listLedger             func(ids.WalletID, string, int) (app.LedgerPage, error)
	reconcileWallet        func(ids.WalletID) (app.Reconciliation, error)
	submitTransaction      func(context.Context, app.SubmitInput) (app.SubmitResult, error)
	getTransaction         func(app.Caller, ids.TransactionID) (wager.State, error)
	getProviderTransaction func(app.Caller, ids.ProviderID, ids.ExternalTransactionID) (wager.State, error)
}

func (f *fakeService) OpenWallet(_ context.Context, in app.OpenWalletInput) (wallet.State, error) {
	f.calls = append(f.calls, "OpenWallet")
	return f.openWallet(in)
}

func (f *fakeService) GetWallet(_ context.Context, id ids.WalletID) (wallet.State, error) {
	f.calls = append(f.calls, "GetWallet")
	return f.getWallet(id)
}

func (f *fakeService) ListLedger(_ context.Context, id ids.WalletID, cursor string, limit int) (app.LedgerPage, error) {
	f.calls = append(f.calls, "ListLedger")
	return f.listLedger(id, cursor, limit)
}

func (f *fakeService) ReconcileWallet(_ context.Context, id ids.WalletID) (app.Reconciliation, error) {
	f.calls = append(f.calls, "ReconcileWallet")
	return f.reconcileWallet(id)
}

func (f *fakeService) SubmitTransaction(ctx context.Context, in app.SubmitInput) (app.SubmitResult, error) {
	f.calls = append(f.calls, "SubmitTransaction")
	return f.submitTransaction(ctx, in)
}

func (f *fakeService) GetTransaction(_ context.Context, caller app.Caller, id ids.TransactionID) (wager.State, error) {
	f.calls = append(f.calls, "GetTransaction")
	return f.getTransaction(caller, id)
}

func (f *fakeService) GetProviderTransaction(_ context.Context, caller app.Caller, providerID ids.ProviderID, externalID ids.ExternalTransactionID) (wager.State, error) {
	f.calls = append(f.calls, "GetProviderTransaction")
	return f.getProviderTransaction(caller, providerID, externalID)
}

type fakeVerifier struct {
	callers map[string]app.Caller
}

func (v fakeVerifier) Verify(_ context.Context, token string) (app.Caller, error) {
	caller, known := v.callers[token]
	if !known {
		return app.Caller{}, errors.New("signature does not match")
	}
	return caller, nil
}

type fakeReadiness struct {
	failed []string
}

func (r fakeReadiness) NotReady(context.Context) []string {
	return r.failed
}

type harness struct {
	service   *fakeService
	readiness *fakeReadiness
	logs      *logtest.Buffer
	handler   http.Handler
	api       *httpapi.API
}

func parsed[T any](t *testing.T, parse func(string) (T, error), text string) T {
	t.Helper()

	value, err := parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func amount(t *testing.T, text string) money.Money {
	t.Helper()

	m, err := money.Parse(text, parsed(t, money.ParseCurrency, "BRL"))
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return m
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	provider := func(name string) app.Caller {
		return app.Caller{ProviderID: parsed(t, ids.ParseProviderID, name), Scopes: []string{app.ScopeWageringWrite, app.ScopeWageringRead}}
	}
	h := &harness{service: &fakeService{}, readiness: &fakeReadiness{}, logs: &logtest.Buffer{}}
	h.api = httpapi.New(httpapi.Dependencies{
		Service: h.service,
		Verifier: fakeVerifier{callers: map[string]app.Caller{
			providerAToken: provider("provider-a"),
			providerBToken: provider("provider-b"),
			internalToken:  {Scopes: []string{app.ScopeWalletsWrite, app.ScopeWalletsRead, app.ScopeWageringRead}},
		}},
		Readiness: h.readiness,
		Metrics: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("wager_transactions_total 0\n"))
		}),
		Logger:         observability.NewLogger(h.logs, slog.LevelDebug),
		RequestTimeout: 100 * time.Millisecond,
		MaxBodyBytes:   4096,
	})
	h.handler = h.api.Handler()
	return h
}

type call struct {
	method  string
	path    string
	token   string
	body    string
	headers map[string]string
}

func (h *harness) do(c call) *httptest.ResponseRecorder {
	request := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	for name, value := range c.headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func submit(token, body string) call {
	return call{
		method: http.MethodPost, path: "/wagering/transactions", token: token, body: body,
		headers: map[string]string{"Idempotency-Key": "provider-a:transaction-123"},
	}
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", recorder.Body.String(), err)
	}
	return body
}

func wantProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if recorder.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	body := decode(t, recorder)
	if body["code"] != code || body["status"] != float64(status) {
		t.Fatalf("problem = %v, want code %s and status %d", body, code, status)
	}
	if body["type"] == nil || body["title"] != http.StatusText(status) || body["correlationId"] != recorder.Header().Get("X-Correlation-Id") {
		t.Fatalf("problem = %v, want type, title and the correlation id of the response", body)
	}
	if len(body) != 5 {
		t.Fatalf("problem = %v, want exactly type, title, status, code and correlationId", body)
	}
}

func transactionState(t *testing.T, status wager.Status) wager.State {
	t.Helper()

	state := wager.State{
		ID:       parsed(t, ids.ParseTransactionID, transactionUUID),
		Kind:     wager.Bet,
		Status:   status,
		WalletID: parsed(t, ids.ParseWalletID, walletUUID),
		PlayerID: parsed(t, ids.ParsePlayerID, playerUUID),
		Amount:   amount(t, "25.00"),
		External: wager.External{
			ProviderID: parsed(t, ids.ParseProviderID, "provider-a"),
			ExternalID: parsed(t, ids.ParseExternalTransactionID, "transaction-123"),
			RoundID:    parsed(t, ids.ParseRoundID, "round-987"),
			GameID:     parsed(t, ids.ParseGameID, "fortune-chimp"),
		},
		CreatedAt: createdAt,
	}
	switch status {
	case wager.Processed:
		state.ResultBalance = amount(t, "975.00")
		state.CompletedAt = createdAt
	case wager.Rejected:
		state.FailureCode = failure.InsufficientFunds
		state.CompletedAt = createdAt
	case wager.Failed:
		state.FailureCode = failure.ProcessingFailed
		state.CompletedAt = createdAt
	case wager.PendingReference:
		state.ReferenceExpiresAt = createdAt.Add(5 * time.Minute)
	}
	return state
}

func TestInternalErrorDoesNotLeakDetails(t *testing.T) {
	h := newHarness(t)
	h.service.getWallet = func(ids.WalletID) (wallet.State, error) {
		return wallet.State{}, errors.New("dial postgres://wallet:hunter2@db:5432 failed")
	}

	recorder := h.do(call{
		method: http.MethodGet, path: "/wallets/" + walletUUID, token: internalToken,
		headers: map[string]string{"X-Correlation-Id": "request-42"},
	})
	wantProblem(t, recorder, http.StatusInternalServerError, httpapi.CodeInternalError)
	if !strings.Contains(h.logs.String(), `"correlationId":"request-42"`) {
		t.Fatalf("logs = %s, want the correlation id of the request", h.logs.String())
	}
	if strings.Contains(recorder.Body.String(), "hunter2") || strings.Contains(recorder.Body.String(), "postgres") {
		t.Fatalf("body leaks the internal error: %s", recorder.Body.String())
	}
	if !strings.Contains(h.logs.String(), "request failed") {
		t.Fatal("the internal error was not logged")
	}
}

func TestAuthentication(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		status        int
		code          string
	}{
		{"no header", "", http.StatusUnauthorized, httpapi.CodeUnauthorized},
		{"another scheme", "Basic dXNlcjpwYXNz", http.StatusUnauthorized, httpapi.CodeUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized, httpapi.CodeUnauthorized},
		{"invalid token", "Bearer forged", http.StatusUnauthorized, httpapi.CodeUnauthorized},
		{"provider opening a wallet", "Bearer " + providerAToken, http.StatusForbidden, httpapi.CodeInsufficientScope},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(call{
				method: http.MethodPost, path: "/wallets",
				body:    `{"playerId":"` + playerUUID + `","initialBalance":{"amount":"1000.00","currency":"BRL"}}`,
				headers: map[string]string{"Authorization": tt.authorization},
			})
			wantProblem(t, recorder, tt.status, tt.code)
			if len(h.service.calls) != 0 {
				t.Fatalf("the use case was called: %v", h.service.calls)
			}
		})
	}

	t.Run("internal service sending a bet", func(t *testing.T) {
		h := newHarness(t)
		wantProblem(t, h.do(submit(internalToken, betBody)), http.StatusForbidden, httpapi.CodeInsufficientScope)
		if len(h.service.calls) != 0 {
			t.Fatalf("the use case was called: %v", h.service.calls)
		}
	})
}

func TestSubmitRejectsInvalidRequests(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(betBody, old, new, 1) }

	tests := []struct {
		name string
		body string
		key  string
		code failure.Code
	}{
		{"malformed json", `{"providerId":`, "key-1", failure.MalformedRequest},
		{"unknown field", replace(`"kind"`, `"bonus":true,"kind"`), "key-1", failure.MalformedRequest},
		{"two documents", betBody + betBody, "key-1", failure.MalformedRequest},
		{"body above the limit", replace(`"round-987"`, `"`+strings.Repeat("r", 5000)+`"`), "key-1", failure.MalformedRequest},
		{"missing wallet", replace(`"walletId":"`+walletUUID+`",`, ""), "key-1", failure.MalformedRequest},
		{"missing money", replace(`,"money":{"amount":"25.00","currency":"BRL"}`, ""), "key-1", failure.MalformedRequest},
		{"missing kind", replace(`"kind":"BET",`, ""), "key-1", failure.MalformedRequest},
		{"malformed wallet id", replace(walletUUID, "not-a-uuid"), "key-1", failure.InvalidIdentifier},
		{"numeric amount", replace(`"25.00"`, `25.00`), "key-1", failure.InvalidMoney},
		{"amount without cents", replace(`"25.00"`, `"25"`), "key-1", failure.InvalidMoney},
		{"negative amount", replace(`"25.00"`, `"-25.00"`), "key-1", failure.InvalidMoney},
		{"unknown currency", replace(`"BRL"`, `"XXX"`), "key-1", failure.InvalidMoney},
		{"unknown kind", replace(`"BET"`, `"BONUS"`), "key-1", failure.UnsupportedKind},
		{"missing idempotency key", betBody, "", failure.MissingIdempotencyKey},
		{"idempotency key too long", betBody, strings.Repeat("k", 256), failure.InvalidIdentifier},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			c := submit(providerAToken, tt.body)
			c.headers["Idempotency-Key"] = tt.key

			wantProblem(t, h.do(c), http.StatusBadRequest, string(tt.code))
			if len(h.service.calls) != 0 {
				t.Fatalf("the use case was called: %v", h.service.calls)
			}
		})
	}
}

func TestSubmitStatusByOutcome(t *testing.T) {
	tests := []struct {
		name        string
		result      app.SubmitResult
		err         error
		status      int
		problemCode string
		wantBody    map[string]any
	}{
		{name: "processed", result: app.SubmitResult{Transaction: transactionState(t, wager.Processed)}, status: http.StatusOK,
			wantBody: map[string]any{"status": "PROCESSED", "idempotentReplay": false, "balance": map[string]any{"amount": "975.00", "currency": "BRL"}}},
		{name: "replay of processed", result: app.SubmitResult{Transaction: transactionState(t, wager.Processed), IdempotentReplay: true}, status: http.StatusOK,
			wantBody: map[string]any{"status": "PROCESSED", "idempotentReplay": true, "balance": map[string]any{"amount": "975.00", "currency": "BRL"}}},
		{name: "waiting for the reference", result: app.SubmitResult{Transaction: transactionState(t, wager.PendingReference)}, status: http.StatusAccepted,
			wantBody: map[string]any{"status": "PENDING_REFERENCE", "idempotentReplay": false, "referenceExpiresAt": "2026-01-01T12:05:00Z"}},
		{name: "rejected", result: app.SubmitResult{Transaction: transactionState(t, wager.Rejected)}, status: http.StatusUnprocessableEntity,
			wantBody: map[string]any{"status": "REJECTED", "idempotentReplay": false, "failureCode": "INSUFFICIENT_FUNDS"}},
		{name: "replay of rejected", result: app.SubmitResult{Transaction: transactionState(t, wager.Rejected), IdempotentReplay: true}, status: http.StatusUnprocessableEntity,
			wantBody: map[string]any{"status": "REJECTED", "idempotentReplay": true, "failureCode": "INSUFFICIENT_FUNDS"}},
		{name: "replay of failed", result: app.SubmitResult{Transaction: transactionState(t, wager.Failed), IdempotentReplay: true}, status: http.StatusInternalServerError,
			wantBody: map[string]any{"status": "FAILED", "idempotentReplay": true, "failureCode": "PROCESSING_FAILED"}},
		{name: "invalid for the kind", err: failure.InvalidInputError{Code: failure.InvalidAmountForKind}, status: http.StatusBadRequest, problemCode: "INVALID_AMOUNT_FOR_KIND"},
		{name: "unknown wallet", err: failure.InvalidInputError{Code: failure.WalletNotFound}, status: http.StatusNotFound, problemCode: "WALLET_NOT_FOUND"},
		{name: "key reused", err: app.ConflictError{Code: app.ConflictIdempotencyKeyReused}, status: http.StatusConflict, problemCode: "IDEMPOTENCY_KEY_REUSED"},
		{name: "operation already registered", err: app.ConflictError{Code: app.ConflictTransactionAlreadyRegistered}, status: http.StatusConflict, problemCode: "TRANSACTION_ALREADY_REGISTERED"},
		{name: "database unavailable", err: errors.Join(app.ErrTransient, errors.New("connection")), status: http.StatusServiceUnavailable, problemCode: httpapi.CodeServiceUnavailable},
		{name: "unexpected", err: errors.New("boom"), status: http.StatusInternalServerError, problemCode: httpapi.CodeInternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.service.submitTransaction = func(context.Context, app.SubmitInput) (app.SubmitResult, error) {
				return tt.result, tt.err
			}

			recorder := h.do(submit(providerAToken, betBody))
			if tt.problemCode != "" {
				wantProblem(t, recorder, tt.status, tt.problemCode)
				return
			}

			if recorder.Code != tt.status || recorder.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status, type = %d, %q, want %d as application/json", recorder.Code, recorder.Header().Get("Content-Type"), tt.status)
			}
			tt.wantBody["transactionId"] = transactionUUID
			got := decode(t, recorder)
			if len(got) != len(tt.wantBody) {
				t.Fatalf("body = %v, want exactly %v", got, tt.wantBody)
			}
			for key, want := range tt.wantBody {
				if !jsonEqual(got[key], want) {
					t.Fatalf("%s = %v, want %v (body %v)", key, got[key], want, got)
				}
			}
		})
	}
}

func jsonEqual(got, want any) bool {
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	return string(a) == string(b)
}

func TestSubmitForAnotherProviderIsForbidden(t *testing.T) {
	h := newHarness(t)

	wantProblem(t, h.do(submit(providerBToken, betBody)), http.StatusForbidden, httpapi.CodeProviderMismatch)
	if len(h.service.calls) != 0 {
		t.Fatalf("the use case was called: %v", h.service.calls)
	}
}

func TestGetTransaction(t *testing.T) {
	h := newHarness(t)
	notFound := failure.InvalidInputError{Code: failure.TransactionNotFound}
	h.service.getTransaction = func(caller app.Caller, id ids.TransactionID) (wager.State, error) {
		if id.String() != transactionUUID || !caller.CanReadProvider(parsed(t, ids.ParseProviderID, "provider-a")) {
			return wager.State{}, notFound
		}
		return transactionState(t, wager.PendingReference), nil
	}
	get := func(id, token string) call {
		return call{method: http.MethodGet, path: "/wagering/transactions/" + id, token: token}
	}

	found := h.do(get(transactionUUID, providerAToken))
	body := decode(t, found)
	if found.Code != http.StatusOK || body["status"] != "PENDING_REFERENCE" || body["referenceExpiresAt"] != "2026-01-01T12:05:00Z" {
		t.Fatalf("response = %d %v, want 200 with the pending status and its deadline", found.Code, body)
	}
	if body["kind"] != "BET" || body["providerId"] != "provider-a" || body["createdAt"] != "2026-01-01T12:00:00Z" || body["completedAt"] != nil {
		t.Fatalf("body = %v, want the kind, the provider and the creation time in UTC", body)
	}

	unknown := h.do(get(walletUUID, providerAToken))
	ofAnother := h.do(get(transactionUUID, providerBToken))
	wantProblem(t, unknown, http.StatusNotFound, "TRANSACTION_NOT_FOUND")
	wantProblem(t, ofAnother, http.StatusNotFound, "TRANSACTION_NOT_FOUND")
	strip := func(r *httptest.ResponseRecorder) string {
		body := decode(t, r)
		delete(body, "correlationId")
		encoded, _ := json.Marshal(body)
		return string(encoded)
	}
	if strip(unknown) != strip(ofAnother) {
		t.Fatalf("bodies differ: %s and %s, want the same answer for unknown and for another provider", unknown.Body.String(), ofAnother.Body.String())
	}

	wantProblem(t, h.do(get("not-a-uuid", providerAToken)), http.StatusBadRequest, "INVALID_IDENTIFIER")
}

func TestGetProviderTransaction(t *testing.T) {
	h := newHarness(t)
	h.service.getProviderTransaction = func(app.Caller, ids.ProviderID, ids.ExternalTransactionID) (wager.State, error) {
		return transactionState(t, wager.Rejected), nil
	}
	const path = "/providers/provider-a/wagering/transactions/transaction-123"

	for name, token := range map[string]string{"owner": providerAToken, "internal service": internalToken} {
		recorder := h.do(call{method: http.MethodGet, path: path, token: token})
		if body := decode(t, recorder); recorder.Code != http.StatusOK || body["failureCode"] != "INSUFFICIENT_FUNDS" {
			t.Fatalf("%s: response = %d %v, want 200 with the failure code", name, recorder.Code, body)
		}
	}

	h.service.calls = nil
	wantProblem(t, h.do(call{method: http.MethodGet, path: path, token: providerBToken}), http.StatusForbidden, httpapi.CodeProviderMismatch)
	if len(h.service.calls) != 0 {
		t.Fatalf("the use case was called for the path of another provider: %v", h.service.calls)
	}
}

func TestHealthAndMetricsArePublic(t *testing.T) {
	h := newHarness(t)

	live := h.do(call{method: http.MethodGet, path: "/health/live"})
	ready := h.do(call{method: http.MethodGet, path: "/health/ready"})
	metrics := h.do(call{method: http.MethodGet, path: "/metrics"})
	if live.Code != http.StatusOK || ready.Code != http.StatusOK || metrics.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d, %d, want 200 for the three public routes", live.Code, ready.Code, metrics.Code)
	}
	if !strings.Contains(metrics.Body.String(), "wager_transactions_total") {
		t.Fatalf("/metrics = %q, want the collector output", metrics.Body.String())
	}

	h.readiness.failed = []string{"sqs"}
	unavailable := h.do(call{method: http.MethodGet, path: "/health/ready"})
	if strings.TrimSpace(unavailable.Body.String()) != `{"status":"unavailable","failed":["sqs"]}` || unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness = %d %s, want 503 naming only the dependency", unavailable.Code, unavailable.Body.String())
	}
	if again := h.do(call{method: http.MethodGet, path: "/health/live"}); again.Code != http.StatusOK {
		t.Fatalf("liveness = %d while a dependency is down, want 200", again.Code)
	}
}

func TestEveryBusinessRouteRequiresAScope(t *testing.T) {
	h := newHarness(t)
	public := map[string]bool{"GET /health/live": true, "GET /health/ready": true, "GET /metrics": true}
	scopes := map[string]string{
		"POST /wallets":                              app.ScopeWalletsWrite,
		"GET /wallets/{walletId}":                    app.ScopeWalletsRead,
		"GET /wallets/{walletId}/ledger":             app.ScopeWalletsRead,
		"POST /wallets/{walletId}/reconciliation":    app.ScopeWalletsRead,
		"POST /wagering/transactions":                app.ScopeWageringWrite,
		"GET /wagering/transactions/{transactionId}": app.ScopeWageringRead,
		"GET /providers/{providerId}/wagering/transactions/{externalTransactionId}": app.ScopeWageringRead,
	}

	routes := h.api.Routes()
	if len(routes) != 10 {
		t.Fatalf("routes = %d, want the ten of the contract", len(routes))
	}
	for _, route := range routes {
		name := route.Method + " " + route.Path
		if route.Public != public[name] {
			t.Errorf("%s: public = %t, want %t", name, route.Public, public[name])
		}
		if route.Scope != scopes[name] {
			t.Errorf("%s: scope = %q, want %q", name, route.Scope, scopes[name])
		}
		if route.Public {
			continue
		}
		path := strings.NewReplacer("{walletId}", walletUUID, "{transactionId}", transactionUUID,
			"{providerId}", "provider-a", "{externalTransactionId}", "transaction-123").Replace(route.Path)
		wantProblem(t, h.do(call{method: route.Method, path: path}), http.StatusUnauthorized, httpapi.CodeUnauthorized)
	}
}
