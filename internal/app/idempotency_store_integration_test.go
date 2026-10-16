//go:build integration

package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/app/internal/platform/idempotency"
	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/store/storetest"
)

// testDB is set once by TestMain and shared by every test in this package.
var testDB *storetest.DB

func TestMain(m *testing.M) {
	os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db },
		storetest.WithMigration(queue.MigrateUp),
	))
}

func TestIdempotencyStoreLifecycle(t *testing.T) {
	pool := testDB.Pool(t)
	s := newIdempotencyStore(pool)
	ctx := context.Background()
	hash := []byte("hash-1")

	if ok, err := s.Reserve(ctx, "alice", "k1", hash); err != nil || !ok {
		t.Fatalf("first Reserve = %v, %v; want true", ok, err)
	}
	if ok, err := s.Reserve(ctx, "alice", "k1", hash); err != nil || ok {
		t.Fatalf("second Reserve = %v, %v; want false", ok, err)
	}

	rec, err := s.Get(ctx, "alice", "k1")
	if err != nil || rec.Status != 0 || string(rec.RequestHash) != "hash-1" {
		t.Fatalf("Get while in flight = %+v, %v", rec, err)
	}

	if err := s.Complete(ctx, "alice", "k1", 201, []byte("stored")); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	rec, err = s.Get(ctx, "alice", "k1")
	if err != nil || rec.Status != 201 || string(rec.Body) != "stored" {
		t.Fatalf("Get after Complete = %+v, %v", rec, err)
	}

	if err := s.Release(ctx, "alice", "k1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := s.Get(ctx, "alice", "k1"); !errors.Is(err, idempotency.ErrNotFound) {
		t.Errorf("Get after Release err = %v, want ErrNotFound", err)
	}
	if ok, err := s.Reserve(ctx, "alice", "k1", hash); err != nil || !ok {
		t.Errorf("Reserve after Release = %v, %v; want true", ok, err)
	}
}

func TestIdempotencyStoreScopesAreIndependent(t *testing.T) {
	pool := testDB.Pool(t)
	s := newIdempotencyStore(pool)
	ctx := context.Background()

	for _, scope := range []string{"alice", "bob"} {
		if ok, err := s.Reserve(ctx, scope, "same-key", []byte("h")); err != nil || !ok {
			t.Errorf("Reserve for %s = %v, %v; want true", scope, ok, err)
		}
	}
}

func TestIdempotencyStoreReserveIsExclusiveUnderConcurrency(t *testing.T) {
	pool := testDB.Pool(t)
	s := newIdempotencyStore(pool)

	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.Reserve(context.Background(), "alice", "contested", []byte("h"))
			if err == nil && ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Errorf("%d Reserve calls won the key, want exactly 1", got)
	}
}

func TestIdempotencyStoreDeleteExpired(t *testing.T) {
	pool := testDB.Pool(t)
	s := newIdempotencyStore(pool)
	ctx := context.Background()

	for _, key := range []string{"old", "fresh"} {
		if _, err := s.Reserve(ctx, "alice", key, []byte("h")); err != nil {
			t.Fatalf("Reserve %s: %v", key, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '2 days' WHERE key = 'old'`); err != nil {
		t.Fatalf("age key: %v", err)
	}

	deleted, err := s.DeleteExpired(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpired = %d, %v; want 1", deleted, err)
	}
	if _, err := s.Get(ctx, "alice", "old"); !errors.Is(err, idempotency.ErrNotFound) {
		t.Errorf("expired key still present: %v", err)
	}
	if _, err := s.Get(ctx, "alice", "fresh"); err != nil {
		t.Errorf("fresh key was deleted: %v", err)
	}
}
