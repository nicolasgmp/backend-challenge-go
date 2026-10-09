package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
)

const (
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeInsufficientScope  = "INSUFFICIENT_SCOPE"
	CodeProviderMismatch   = "PROVIDER_MISMATCH"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	CodeInternalError      = "INTERNAL_ERROR"

	retryAfterSeconds = "1"
)

type problem struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Status        int    `json:"status"`
	Code          string `json:"code"`
	CorrelationID string `json:"correlationId"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code string) {
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", retryAfterSeconds)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:          "about:blank",
		Title:         http.StatusText(status),
		Status:        status,
		Code:          code,
		CorrelationID: correlationID(r.Context()),
	})
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := classify(err)
	if status == http.StatusInternalServerError {
		a.deps.Logger.ErrorContext(r.Context(), "request failed", slog.String("error", err.Error()))
	}
	writeProblem(w, r, status, code)
}

func classify(err error) (int, string) {
	var invalid failure.InvalidInputError
	var conflict app.ConflictError
	switch {
	case errors.As(err, &invalid):
		return invalidInputStatus(invalid.Code), string(invalid.Code)
	case errors.As(err, &conflict):
		return http.StatusConflict, conflict.Code
	case errors.Is(err, app.ErrForbidden):
		return http.StatusForbidden, CodeProviderMismatch
	case isUnavailable(err):
		return http.StatusServiceUnavailable, CodeServiceUnavailable
	default:
		return http.StatusInternalServerError, CodeInternalError
	}
}

func invalidInputStatus(code failure.Code) int {
	if code == failure.WalletNotFound || code == failure.TransactionNotFound {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func isUnavailable(err error) bool {
	for _, transient := range []error{app.ErrTransient, app.ErrUniqueViolation, context.DeadlineExceeded, context.Canceled} {
		if errors.Is(err, transient) {
			return true
		}
	}
	return false
}
