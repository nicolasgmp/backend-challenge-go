package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"jungle-gaming-challeng/internal/app"
)

const (
	codeUniqueViolation      = "23505"
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
	codeLockNotAvailable     = "55P03"
	classConnectionException = "08"
	classOperatorIntervened  = "57"
)

var ErrNoTransaction = errors.New("postgres: called outside a transaction")

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return mapCode(pgErr.Code, pgErr.ConstraintName)
	}

	if isConnectionFailure(err) {
		return fmt.Errorf("%w: connection", app.ErrTransient)
	}
	return fmt.Errorf("postgres: %w", err)
}

func isConnectionFailure(err error) bool {
	var netErr net.Error
	var connectErr *pgconn.ConnectError
	if errors.As(err, &netErr) || errors.As(err, &connectErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func mapCode(code, constraint string) error {
	switch {
	case code == codeUniqueViolation:
		return fmt.Errorf("%w: %s", app.ErrUniqueViolation, constraint)
	case code == codeLockNotAvailable:
		return fmt.Errorf("%w: %w", app.ErrTransient, app.ErrLockTimeout)
	case code == codeSerializationFailure, code == codeDeadlockDetected,
		strings.HasPrefix(code, classConnectionException), strings.HasPrefix(code, classOperatorIntervened):
		return fmt.Errorf("%w: sqlstate %s", app.ErrTransient, code)
	case constraint != "":
		return fmt.Errorf("postgres: sqlstate %s on %s", code, constraint)
	default:
		return fmt.Errorf("postgres: sqlstate %s", code)
	}
}
