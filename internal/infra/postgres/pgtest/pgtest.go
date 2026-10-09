//go:build integration

package pgtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	Image         = "postgres:17.6-alpine"
	port          = "5432/tcp"
	user          = "wallet"
	localPassword = "local-test-password"
	template      = "migrated"
)

var (
	baseURL   string
	databases atomic.Int64
)

type Migration struct {
	Name string
	SQL  string
}

func Main(m *testing.M, migrationsDir string) {
	ctx := context.Background()

	ctr, err := testcontainers.Run(ctx, Image,
		testcontainers.WithExposedPorts(port),
		testcontainers.WithEnv(map[string]string{"POSTGRES_USER": user, "POSTGRES_PASSWORD": localPassword}),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(time.Minute),
		),
	)
	if err == nil {
		err = prepare(ctx, ctr, migrationsDir)
	}
	code := 1
	if err == nil {
		code = m.Run()
	} else {
		fmt.Fprintln(os.Stderr, "pgtest:", err)
	}
	if ctr != nil {
		_ = testcontainers.TerminateContainer(ctr)
	}
	os.Exit(code)
}

func prepare(ctx context.Context, ctr testcontainers.Container, migrationsDir string) error {
	host, err := ctr.Host(ctx)
	if err != nil {
		return err
	}
	mapped, err := ctr.MappedPort(ctx, port)
	if err != nil {
		return err
	}
	baseURL = fmt.Sprintf("postgres://%s:%s@%s:%s/", user, localPassword, host, mapped.Port())

	migrations, err := Migrations(migrationsDir, "up")
	if err != nil {
		return err
	}
	if err := exec(ctx, "postgres", "CREATE DATABASE "+template); err != nil {
		return err
	}
	for _, migration := range migrations {
		if err := exec(ctx, template, migration.SQL); err != nil {
			return fmt.Errorf("%s: %w", migration.Name, err)
		}
	}
	return nil
}

func URL(database string) string {
	return baseURL + database + "?sslmode=disable"
}

func NewDatabase(ctx context.Context, tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	return Connect(ctx, tb, NewDatabaseName(ctx, tb, template))
}

func NewEmptyDatabase(ctx context.Context, tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	return Connect(ctx, tb, NewDatabaseName(ctx, tb, "template0"))
}

func NewDatabaseName(ctx context.Context, tb testing.TB, from string) string {
	tb.Helper()

	name := fmt.Sprintf("test_%d", databases.Add(1))
	if err := exec(ctx, "postgres", fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, from)); err != nil {
		tb.Fatalf("pgtest: create database: %v", err)
	}
	return name
}

func Connect(ctx context.Context, tb testing.TB, database string) *pgxpool.Pool {
	tb.Helper()

	pool, err := pgxpool.New(ctx, URL(database))
	if err != nil {
		tb.Fatalf("pgtest: connect to %s: %v", database, err)
	}
	tb.Cleanup(pool.Close)
	return pool
}

func Migrations(dir, direction string) ([]Migration, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*."+direction+".sql"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no %s migrations in %s", direction, dir)
	}
	slices.Sort(paths)

	var migrations []Migration
	for _, path := range paths {
		sql, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(filepath.Base(path), "."+direction+".sql")
		migrations = append(migrations, Migration{Name: name, SQL: string(sql)})
	}
	return migrations, nil
}

func exec(ctx context.Context, database, sql string) error {
	conn, err := pgx.Connect(ctx, URL(database))
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, sql)
	return err
}
