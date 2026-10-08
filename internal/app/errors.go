package app

import "errors"

var (
	ErrTransient       = errors.New("app: transient failure")
	ErrUniqueViolation = errors.New("app: unique violation")
	ErrNotFound        = errors.New("app: not found")
	ErrIdPUnavailable  = errors.New("app: identity provider unavailable")
)
