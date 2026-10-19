package store

import (
	"context"
	"testing"
	"time"
)

func TestNewPoolRejectsInvalidURL(t *testing.T) {
	if _, err := NewPool(context.Background(), PoolConfig{URL: "postgres://%zz"}); err == nil {
		t.Fatal("expected an error for an unparsable URL")
	}
}

func TestNewPoolAppliesOverrides(t *testing.T) {
	pool, err := NewPool(context.Background(), PoolConfig{
		URL:             "postgres://app:app@127.0.0.1:1/app?sslmode=disable",
		MaxConns:        7,
		MinConns:        0,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	cfg := pool.Config()
	if cfg.MaxConns != 7 {
		t.Errorf("MaxConns = %d, want 7", cfg.MaxConns)
	}
	if cfg.MaxConnLifetime != time.Hour || cfg.MaxConnIdleTime != time.Minute {
		t.Errorf("lifetimes = %v / %v", cfg.MaxConnLifetime, cfg.MaxConnIdleTime)
	}
}

func TestPoolIsLazyAndPingReportsOutage(t *testing.T) {
	ctx := context.Background()
	pool, err := NewPool(ctx, PoolConfig{
		URL: "postgres://app:app@127.0.0.1:1/app?sslmode=disable&connect_timeout=1",
	})
	if err != nil {
		t.Fatalf("NewPool must not need a live database: %v", err)
	}
	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := Pinger(pool)(pingCtx); err == nil {
		t.Fatal("Ping returned nil against a closed port")
	}
}

func TestNewPoolSetsSessionGuards(t *testing.T) {
	pool, err := NewPool(context.Background(), PoolConfig{
		URL:              "postgres://app:app@127.0.0.1:1/app?sslmode=disable",
		StatementTimeout: 30 * time.Second,
		LockTimeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	params := pool.Config().ConnConfig.RuntimeParams
	if got := params["statement_timeout"]; got != "30000" {
		t.Errorf("statement_timeout = %q, want 30000", got)
	}
	if got := params["lock_timeout"]; got != "5000" {
		t.Errorf("lock_timeout = %q, want 5000", got)
	}
	if _, ok := params["idle_in_transaction_session_timeout"]; ok {
		t.Error("idle_in_transaction_session_timeout set although zero")
	}
}
