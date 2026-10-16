// Package store provides Postgres pooling, transaction helpers, and driver
// error classification. It takes plain values and never the config package.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig holds plain connection settings. Zero values keep the pgx
// defaults. Query execution mode for PgBouncer is set in the URL itself, for
// example ?default_query_exec_mode=cache_describe (see the ADR).
type PoolConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// NewPool builds a pgx pool. It does not connect eagerly: the process can start
// while the database is down, and readiness reports the outage until it is back.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("store: create pool: %w", err)
	}
	return pool, nil
}

// Pinging is the part of a pool that readiness needs. *pgxpool.Pool satisfies it.
type Pinging interface {
	Ping(ctx context.Context) error
}

// Pinger returns a readiness check that pings the database. Its signature
// matches httpx.Check, so it can be passed to httpx.Readyz directly.
func Pinger(pool Pinging) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("store: ping: %w", err)
		}
		return nil
	}
}
