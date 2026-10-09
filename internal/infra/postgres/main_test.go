//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/infra/postgres"
	"jungle-gaming-challeng/internal/infra/postgres/pgtest"
)

const (
	walletA  = "0192f291-27dd-7d3f-8071-5f8685deef31"
	walletB  = "0192f291-27dd-7d3f-8071-5f8685deef32"
	playerA  = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"
	betA     = "0192f298-345e-7e38-af88-e43f851a8191"
	refundA  = "0192f298-345e-7e38-af88-e43f851a8192"
	otherTxA = "0192f298-345e-7e38-af88-e43f851a8193"
	entryA   = "0192f299-0000-7000-8000-000000000001"
	entryB   = "0192f299-0000-7000-8000-000000000002"
	eventA   = "0192f2a0-0000-7000-8000-000000000001"
)

func TestMain(m *testing.M) {
	pgtest.Main(m, "../../../migrations")
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()

	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func refused(t *testing.T, pool *pgxpool.Pool, want string, sql string, args ...any) {
	t.Helper()

	_, err := pool.Exec(context.Background(), sql, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("exec %q: err = %v, want the database to refuse it", sql, err)
	}
	if pgErr.ConstraintName != want && pgErr.Message != want {
		t.Fatalf("exec %q: refused by %q (%s), want %q", sql, pgErr.ConstraintName, pgErr.Message, want)
	}
}

func insertWallet(t *testing.T, pool *pgxpool.Pool, id string, balance int64) {
	t.Helper()

	exec(t, pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'BRL', $3, 1, now(), now())`, id, id, balance)
}

const insertExternal = `INSERT INTO wager_transactions
	(id, origin, kind, status, wallet_id, player_id, amount, currency, provider_id, external_transaction_id,
	 idempotency_key, payload_hash, round_id, game_id, reference_external_transaction_id, reference_transaction_id,
	 failure_code, result_balance, reference_expires_at, next_attempt_at, correlation_id, created_at, updated_at, completed_at)
	VALUES ($1, 'EXTERNAL', $2, $3, $4, $4, 2500, 'BRL', $5, $6, $7, 'hash', 'round-1', 'game-1', $8, $9,
	 $10, $11, $12, $12, 'correlation-1', now(), now(), now())`

type externalRow struct {
	id, kind, status, wallet, provider, externalID, key string
	referenceExternalID, referenceID, failureCode       any
	resultBalance                                       any
	deadline                                            any
}

func (r externalRow) args() []any {
	return []any{r.id, r.kind, r.status, r.wallet, r.provider, r.externalID, r.key,
		r.referenceExternalID, r.referenceID, r.failureCode, r.resultBalance, r.deadline}
}

func betRow(id, externalID string) externalRow {
	return externalRow{
		id: id, kind: "BET", status: "PROCESSED", wallet: walletA,
		provider: "provider-a", externalID: externalID, key: "key-" + externalID, resultBalance: int64(97500),
	}
}

func insertExternalRow(t *testing.T, pool *pgxpool.Pool, row externalRow) {
	t.Helper()

	exec(t, pool, insertExternal, row.args()...)
}

func newRunner(pool *pgxpool.Pool) *postgres.TxRunner {
	return postgres.NewTxRunner(pool, 500*time.Millisecond)
}
