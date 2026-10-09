package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"jungle-gaming-challeng/internal/app"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"unique violation", &pgconn.PgError{Code: "23505", ConstraintName: "wallets_player_currency_key"}, app.ErrUniqueViolation},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, app.ErrTransient},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, app.ErrTransient},
		{"lock timeout", &pgconn.PgError{Code: "55P03"}, app.ErrTransient},
		{"lock timeout is also named", &pgconn.PgError{Code: "55P03"}, app.ErrLockTimeout},
		{"connection failure", &pgconn.PgError{Code: "08006"}, app.ErrTransient},
		{"server shutting down", &pgconn.PgError{Code: "57P01"}, app.ErrTransient},
		{"network error", &net.OpError{Op: "dial", Err: errors.New("refused")}, app.ErrTransient},
		{"deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), app.ErrTransient},
		{"cancelled", fmt.Errorf("query: %w", context.Canceled), app.ErrTransient},
		{"no rows", fmt.Errorf("scan: %w", pgx.ErrNoRows), app.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapError(tt.err); !errors.Is(got, tt.want) {
				t.Fatalf("mapError = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapErrorKeepsOtherFailuresUnclassified(t *testing.T) {
	failures := []error{
		&pgconn.PgError{Code: "23514", ConstraintName: "wallets_balance_not_negative", Detail: "Failing row contains (1, 2, -100)."},
		&pgconn.PgError{Code: "P0001", Message: "wallet ledger entries are append-only"},
		errors.New("unexpected"),
	}

	for _, failure := range failures {
		got := mapError(failure)
		for _, sentinel := range []error{app.ErrTransient, app.ErrUniqueViolation, app.ErrNotFound} {
			if errors.Is(got, sentinel) {
				t.Fatalf("mapError(%v) = %v, want no sentinel", failure, got)
			}
		}
	}

	if mapError(nil) != nil {
		t.Fatal("mapError(nil) is not nil")
	}
	withDetail := mapError(failures[0]).Error()
	if withDetail != "postgres: sqlstate 23514 on wallets_balance_not_negative" {
		t.Fatalf("message = %q, want the code and the constraint without row values", withDetail)
	}
}
