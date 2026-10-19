package queue

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"example.com/app/internal/platform/store"
)

func newLazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := store.NewPool(context.Background(), store.PoolConfig{
		URL: "postgres://app:app@127.0.0.1:1/app?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNewInsertClientDoesNotNeedALiveDatabase(t *testing.T) {
	client, err := NewInsertClient(newLazyPool(t))
	if err != nil || client == nil {
		t.Fatalf("NewInsertClient = %v, %v", client, err)
	}
}

func TestNewWorkerClientValidatesConfig(t *testing.T) {
	pool := newLazyPool(t)

	if _, err := NewWorkerClient(pool, WorkerConfig{Workers: nil, Concurrency: 5}); err == nil {
		t.Error("expected an error for nil workers")
	}
	if _, err := NewWorkerClient(pool, WorkerConfig{Workers: NewWorkers(), Concurrency: 0}); err == nil {
		t.Error("expected an error for zero concurrency")
	}
}

func TestTotalWorkers(t *testing.T) {
	tests := []struct {
		name string
		cfg  WorkerConfig
		want int
	}{
		{"single queue", WorkerConfig{Concurrency: 10}, 10},
		{"named queues win", WorkerConfig{Concurrency: 10, Queues: map[string]int{"default": 4, "webhooks": 3}}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.TotalWorkers(); got != tt.want {
				t.Errorf("TotalWorkers = %d, want %d", got, tt.want)
			}
		})
	}
}

type tickArgs struct{}

func (tickArgs) Kind() string { return "queue_test.tick" }

type tickWorker struct {
	river.WorkerDefaults[tickArgs]
}

func (tickWorker) Work(context.Context, *river.Job[tickArgs]) error { return nil }

func TestNewWorkerClientAcceptsPeriodicJobs(t *testing.T) {
	workers := NewWorkers()
	river.AddWorker(workers, tickWorker{})

	periodic := river.NewPeriodicJob(
		river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return tickArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true},
	)
	client, err := NewWorkerClient(newLazyPool(t), WorkerConfig{
		Workers:      workers,
		Concurrency:  2,
		PeriodicJobs: []*river.PeriodicJob{periodic},
	})
	if err != nil || client == nil {
		t.Fatalf("NewWorkerClient with periodic jobs = %v, %v", client, err)
	}
}
