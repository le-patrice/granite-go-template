package queue

import (
	"context"
	"testing"

	"example.com/app/internal/platform/store"
)

func TestNewInsertClientDoesNotNeedALiveDatabase(t *testing.T) {
	pool, err := store.NewPool(context.Background(), store.PoolConfig{
		URL: "postgres://app:app@127.0.0.1:1/app?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	client, err := NewInsertClient(pool)
	if err != nil || client == nil {
		t.Fatalf("NewInsertClient = %v, %v", client, err)
	}
}

func TestNewWorkerClientValidatesConfig(t *testing.T) {
	pool, err := store.NewPool(context.Background(), store.PoolConfig{
		URL: "postgres://app:app@127.0.0.1:1/app?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if _, err := NewWorkerClient(pool, WorkerConfig{Workers: nil, Concurrency: 5}); err == nil {
		t.Error("expected an error for nil workers")
	}
	if _, err := NewWorkerClient(pool, WorkerConfig{Workers: NewWorkers(), Concurrency: 0}); err == nil {
		t.Error("expected an error for zero concurrency")
	}
}
