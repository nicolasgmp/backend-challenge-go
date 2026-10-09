package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

type Service interface {
	OpenWallet(ctx context.Context, in app.OpenWalletInput) (wallet.State, error)
	GetWallet(ctx context.Context, id ids.WalletID) (wallet.State, error)
	ListLedger(ctx context.Context, walletID ids.WalletID, cursor string, limit int) (app.LedgerPage, error)
	ReconcileWallet(ctx context.Context, walletID ids.WalletID) (app.Reconciliation, error)
	SubmitTransaction(ctx context.Context, in app.SubmitInput) (app.SubmitResult, error)
	GetTransaction(ctx context.Context, caller app.Caller, id ids.TransactionID) (wager.State, error)
	GetProviderTransaction(ctx context.Context, caller app.Caller, providerID ids.ProviderID, externalID ids.ExternalTransactionID) (wager.State, error)
}

type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (app.Caller, error)
}

type Readiness interface {
	NotReady(ctx context.Context) []string
}

type Dependencies struct {
	Service        Service
	Verifier       TokenVerifier
	Readiness      Readiness
	Metrics        http.Handler
	Logger         *slog.Logger
	RequestTimeout time.Duration
	MaxBodyBytes   int64
}

type API struct {
	deps Dependencies
}

type Route struct {
	Method  string
	Path    string
	Scope   string
	Public  bool
	handler http.HandlerFunc
}

func New(deps Dependencies) *API {
	return &API{deps: deps}
}

func (a *API) Routes() []Route {
	return []Route{
		{Method: http.MethodPost, Path: "/wallets", Scope: app.ScopeWalletsWrite, handler: a.openWallet},
		{Method: http.MethodGet, Path: "/wallets/{walletId}", Scope: app.ScopeWalletsRead, handler: a.getWallet},
		{Method: http.MethodGet, Path: "/wallets/{walletId}/ledger", Scope: app.ScopeWalletsRead, handler: a.listLedger},
		{Method: http.MethodPost, Path: "/wallets/{walletId}/reconciliation", Scope: app.ScopeWalletsRead, handler: a.reconcileWallet},
		{Method: http.MethodPost, Path: "/wagering/transactions", Scope: app.ScopeWageringWrite, handler: a.submitTransaction},
		{Method: http.MethodGet, Path: "/wagering/transactions/{transactionId}", Scope: app.ScopeWageringRead, handler: a.getTransaction},
		{Method: http.MethodGet, Path: "/providers/{providerId}/wagering/transactions/{externalTransactionId}", Scope: app.ScopeWageringRead, handler: a.getProviderTransaction},
		{Method: http.MethodGet, Path: "/health/live", Public: true, handler: a.live},
		{Method: http.MethodGet, Path: "/health/ready", Public: true, handler: a.ready},
		{Method: http.MethodGet, Path: "/metrics", Public: true, handler: a.deps.Metrics.ServeHTTP},
	}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, route := range a.Routes() {
		handler := route.handler
		if !route.Public {
			handler = a.authenticate(route.Scope, handler)
		}
		mux.HandleFunc(route.Method+" "+route.Path, handler)
	}
	return withCorrelation(a.withRecovery(a.withTimeout(notFoundAsProblem(mux))))
}

func notFoundAsProblem(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		if matchesAnotherMethod(mux, r) {
			writeProblem(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed)
			return
		}
		writeProblem(w, r, http.StatusNotFound, CodeNotFound)
	})
}

func matchesAnotherMethod(mux *http.ServeMux, r *http.Request) bool {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "" {
			return true
		}
	}
	return false
}
