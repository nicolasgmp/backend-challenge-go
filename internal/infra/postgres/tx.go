package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
)

type txKey struct{}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type TxRunner struct {
	pool        *pgxpool.Pool
	lockTimeout time.Duration
}

var _ app.TxRunner = (*TxRunner)(nil)

func NewTxRunner(pool *pgxpool.Pool, lockTimeout time.Duration) *TxRunner {
	return &TxRunner{pool: pool, lockTimeout: lockTimeout}
}

func (r *TxRunner) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.run(ctx, pgx.TxOptions{}, fn)
}

func (r *TxRunner) RunReadOnly(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.run(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

func (r *TxRunner) run(ctx context.Context, options pgx.TxOptions, fn func(ctx context.Context) error) error {
	tx, err := r.begin(ctx, options)
	if err != nil {
		return mapError(err)
	}
	defer rollback(ctx, tx)

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}

func (r *TxRunner) begin(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if outer, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return outer.Begin(ctx)
	}
	tx, err := r.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	timeout := strconv.FormatInt(r.lockTimeout.Milliseconds(), 10) + "ms"
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", timeout); err != nil {
		rollback(ctx, tx)
		return nil, err
	}
	return tx, nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(context.WithoutCancel(ctx))
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	if !ok {
		return nil, ErrNoTransaction
	}
	return tx, nil
}

func reader(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}
