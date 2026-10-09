//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/infra/postgres/pgtest"
)

const (
	firstWallet  = "0192f291-27dd-7d3f-8071-5f8685deef41"
	secondWallet = "0192f291-27dd-7d3f-8071-5f8685deef42"
	insertWallet = `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $1, 'BRL', 0, 1, now(), now())`
)

var errBoom = errors.New("boom")

func insertInTx(ctx context.Context, id string) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertWallet, id)
	return mapError(err)
}

func walletCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM wallets`).Scan(&count); err != nil {
		t.Fatalf("count wallets: %v", err)
	}
	return count
}

func TestNewPool(t *testing.T) {
	ctx := context.Background()
	name := pgtest.NewDatabaseName(ctx, t, "template0")
	cfg := Config{URL: pgtest.URL(name), MaxConns: 4, ConnectTimeout: 5 * time.Second, MaxConnLifetime: time.Hour}

	pool, err := NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if pool.Config().MaxConns != 4 {
		t.Fatalf("MaxConns = %d, want 4", pool.Config().MaxConns)
	}
}

func TestNewPoolHidesThePassword(t *testing.T) {
	ctx := context.Background()
	const secret = "s3cr3t-value"

	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{"malformed url", Config{URL: "postgres://wallet:" + secret + "@localhost:port/db", MaxConns: 4}, ErrInvalidConfig},
		{"unreachable server", Config{URL: "postgres://wallet:" + secret + "@127.0.0.1:1/db", MaxConns: 4, ConnectTimeout: time.Second}, app.ErrTransient},
		{"wrong password", Config{URL: strings.Replace(pgtest.URL("postgres"), "local-test-password", secret, 1), MaxConns: 4, ConnectTimeout: 5 * time.Second}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewPool(ctx, tt.cfg)
			if err == nil {
				pool.Close()
				t.Fatal("NewPool succeeded, want an error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("the error message contains the password: %v", err)
			}
		})
	}
}

func TestRunCommitsAndRollsBack(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewDatabase(ctx, t)
	runner := NewTxRunner(pool, time.Second)

	if err := runner.Run(ctx, func(ctx context.Context) error { return insertInTx(ctx, firstWallet) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if walletCount(t, pool) != 1 {
		t.Fatal("a committed write is missing")
	}

	err := runner.Run(ctx, func(ctx context.Context) error {
		if err := insertInTx(ctx, secondWallet); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want %v", err, errBoom)
	}
	if walletCount(t, pool) != 1 {
		t.Fatal("a failed transaction left a write behind")
	}
}

func TestRunRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewDatabase(ctx, t)
	runner := NewTxRunner(pool, time.Second)

	func() {
		defer func() { _ = recover() }()
		_ = runner.Run(ctx, func(ctx context.Context) error {
			if err := insertInTx(ctx, firstWallet); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if walletCount(t, pool) != 0 {
		t.Fatal("a panicking transaction left a write behind")
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections still held after the panic", acquired)
	}
}

func TestNestedRunJoinsTheOuterTransaction(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewDatabase(ctx, t)
	runner := NewTxRunner(pool, time.Second)

	err := runner.Run(ctx, func(ctx context.Context) error {
		if err := insertInTx(ctx, firstWallet); err != nil {
			return err
		}
		inner := runner.Run(ctx, func(ctx context.Context) error {
			if err := insertInTx(ctx, secondWallet); err != nil {
				return err
			}
			return insertInTx(ctx, secondWallet)
		})
		if !errors.Is(inner, app.ErrUniqueViolation) {
			t.Errorf("inner err = %v, want %v", inner, app.ErrUniqueViolation)
		}
		if walletCount(t, pool) != 0 {
			t.Error("the nested transaction committed on its own")
		}
		return insertInTx(ctx, secondWallet)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if walletCount(t, pool) != 2 {
		t.Fatalf("wallets = %d, want the outer write and the write made after the failed inner block", walletCount(t, pool))
	}

	err = runner.Run(ctx, func(ctx context.Context) error {
		if err := runner.Run(ctx, func(ctx context.Context) error { return insertInTx(ctx, "0192f291-27dd-7d3f-8071-5f8685deef43") }); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) || walletCount(t, pool) != 2 {
		t.Fatalf("err = %v, wallets = %d, want the inner write undone with the outer transaction", err, walletCount(t, pool))
	}
}

func TestRepositoryCallOutsideTransaction(t *testing.T) {
	if err := insertInTx(context.Background(), firstWallet); !errors.Is(err, ErrNoTransaction) {
		t.Fatalf("err = %v, want %v", err, ErrNoTransaction)
	}
}

func TestLockTimeoutIsTransient(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewDatabase(ctx, t)
	runner := NewTxRunner(pool, 200*time.Millisecond)
	if _, err := pool.Exec(ctx, insertWallet, firstWallet); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	lock := func(ctx context.Context) error {
		tx, err := transaction(ctx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT id FROM wallets WHERE id = $1 FOR UPDATE`, firstWallet)
		return mapError(err)
	}

	started := time.Now()
	err := runner.Run(ctx, func(ctx context.Context) error {
		if err := lock(ctx); err != nil {
			return err
		}
		waiting := runner.Run(context.Background(), lock)
		if !errors.Is(waiting, app.ErrTransient) {
			t.Errorf("second transaction: err = %v, want %v", waiting, app.ErrTransient)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if waited := time.Since(started); waited < 200*time.Millisecond || waited > 3*time.Second {
		t.Fatalf("waited %s for the lock, want about 200ms", waited)
	}
}

func TestRunReadOnlySeesOneSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewDatabase(ctx, t)
	runner := NewTxRunner(pool, time.Second)

	count := func(ctx context.Context) (int, error) {
		var wallets int
		err := reader(ctx, pool).QueryRow(ctx, `SELECT count(*) FROM wallets`).Scan(&wallets)
		return wallets, err
	}

	err := runner.RunReadOnly(ctx, func(ctx context.Context) error {
		before, err := count(ctx)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, insertWallet, firstWallet); err != nil {
			return err
		}
		after, err := count(ctx)
		if err != nil {
			return err
		}
		if before != 0 || after != 0 {
			t.Errorf("counts inside the snapshot = %d, %d, want 0, 0", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunReadOnly: %v", err)
	}
	if walletCount(t, pool) != 1 {
		t.Fatalf("wallets = %d, want only the concurrent write", walletCount(t, pool))
	}
}

func TestRunReadOnlyRefusesWrites(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewDatabase(ctx, t)
	runner := NewTxRunner(pool, time.Second)

	err := runner.RunReadOnly(ctx, func(ctx context.Context) error { return insertInTx(ctx, firstWallet) })
	if err == nil || walletCount(t, pool) != 0 {
		t.Fatalf("err = %v, wallets = %d, want the write refused", err, walletCount(t, pool))
	}
}
