package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/infra/observability"
)

const (
	headerCorrelationID = "X-Correlation-Id"
	bearerPrefix        = "Bearer "
)

type correlationKey struct{}

type callerKey struct{}

func correlationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

func callerFrom(ctx context.Context) app.Caller {
	caller, _ := ctx.Value(callerKey{}).(app.Caller)
	return caller
}

func withCorrelation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerCorrelationID)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(headerCorrelationID, id)

		ctx := context.WithValue(r.Context(), correlationKey{}, id)
		next.ServeHTTP(w, r.WithContext(observability.WithCorrelationID(ctx, id)))
	})
}

func (a *API) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.deps.Logger.ErrorContext(r.Context(), "handler panicked", slog.String("path", r.URL.Path))
				writeProblem(w, r, http.StatusInternalServerError, CodeInternalError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), a.deps.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *API) authenticate(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, found := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
		if !found {
			writeProblem(w, r, http.StatusUnauthorized, CodeUnauthorized)
			return
		}
		caller, err := a.deps.Verifier.Verify(r.Context(), token)
		if err != nil {
			writeProblem(w, r, http.StatusUnauthorized, CodeUnauthorized)
			return
		}
		if !caller.HasScope(scope) {
			writeProblem(w, r, http.StatusForbidden, CodeInsufficientScope)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	}
}
