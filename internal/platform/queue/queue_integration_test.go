//go:build integration

package queue_test

import (
	"context"
	"os"
	"testing"

	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/store/storetest"
)

// testDB is set once by TestMain and shared by every test in this package.
var testDB *storetest.DB

func TestMain(m *testing.M) {
	os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db }))
}

func riverTableExists(t *testing.T) bool {
	t.Helper()
	pool := testDB.Pool(t)
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'river_job')`).Scan(&exists)
	if err != nil {
		t.Fatalf("check river_job: %v", err)
	}
	return exists
}

func TestMigrateUpDownUp(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()

	if riverTableExists(t) {
		t.Fatal("river_job exists before any River migration ran")
	}
	if err := queue.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	if !riverTableExists(t) {
		t.Fatal("river_job is missing after MigrateUp")
	}
	if err := queue.MigrateUp(ctx, pool); err != nil {
		t.Errorf("a second MigrateUp must be a no-op: %v", err)
	}
	if err := queue.MigrateDown(ctx, pool); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	if riverTableExists(t) {
		t.Fatal("river_job still exists after MigrateDown")
	}
	if err := queue.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp after down: %v", err)
	}
}
