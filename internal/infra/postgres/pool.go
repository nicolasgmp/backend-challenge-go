package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidConfig = errors.New("postgres: invalid configuration")

type Config struct {
	URL             string
	MaxConns        int32
	ConnectTimeout  time.Duration
	MaxConnLifetime time.Duration
}

func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, mapError(err)
	}
	return pool, nil
}
