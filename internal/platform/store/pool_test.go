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
