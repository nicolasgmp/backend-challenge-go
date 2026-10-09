package app

import "errors"

const (
	ConflictIdempotencyKeyReused         = "IDEMPOTENCY_KEY_REUSED"
	ConflictTransactionAlreadyRegistered = "TRANSACTION_ALREADY_REGISTERED"
	ConflictWalletAlreadyExists          = "WALLET_ALREADY_EXISTS"
)

var (
	ErrTransient       = errors.New("app: transient failure")
	ErrLockTimeout     = errors.New("app: lock wait timed out")
	ErrUniqueViolation = errors.New("app: unique violation")
	ErrNotFound        = errors.New("app: not found")
	ErrForbidden       = errors.New("app: forbidden")
	ErrIdPUnavailable  = errors.New("app: identity provider unavailable")
)

type ConflictError struct {
	Code string
}

func (e ConflictError) Error() string {
	return "conflict: " + e.Code
}
