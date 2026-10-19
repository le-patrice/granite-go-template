// Package queue wraps River: an insert-only client for request paths, a
// worker client for the worker binary, and River's own migrations. It takes
// plain values and never the config package.
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// migrateDownMaxSteps is larger than the number of River migrations, so a
// down run removes River's schema entirely.
const migrateDownMaxSteps = 100

// NewInsertClient returns a client that can only insert jobs. API processes use
// it with InsertTx so a business write and its job commit in one transaction.
func NewInsertClient(pool *pgxpool.Pool) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("queue: insert client: %w", err)
	}
	return client, nil
}

// NewWorkers returns an empty worker bundle for features to register into.
func NewWorkers() *river.Workers {
	return river.NewWorkers()
}

// WorkerConfig configures NewWorkerClient.
type WorkerConfig struct {
	Workers     *river.Workers
	Concurrency int
	// Queues optionally replaces the single default queue with named queues and
	// their max concurrent jobs. Jobs choose a queue through InsertOpts.
	Queues map[string]int
	// PeriodicJobs are scheduled by River. River elects one leader across all
	// worker replicas, so a periodic job is enqueued once per tick, not once per
	// replica. Use river.PeriodicJobOpts{RunOnStart: true} for catch-up work.
	PeriodicJobs []*river.PeriodicJob
	Logger       *slog.Logger
}

// TotalWorkers returns how many jobs may run at once across all queues, which
// is the number of database connections the workers need.
func (c WorkerConfig) TotalWorkers() int {
	if len(c.Queues) == 0 {
		return c.Concurrency
	}
	total := 0
	for _, n := range c.Queues {
		total += n
	}
	return total
}

// NewWorkerClient returns a client that fetches and runs jobs on the default
// queue, or on the named queues in cfg.Queues. The pool should use the direct database URL because River relies on
// LISTEN/NOTIFY.
func NewWorkerClient(pool *pgxpool.Pool, cfg WorkerConfig) (*river.Client[pgx.Tx], error) {
	if cfg.Workers == nil || cfg.Concurrency < 1 {
		return nil, errors.New("queue: workers and a positive concurrency are required")
	}
	queues := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: cfg.Concurrency},
	}
	if len(cfg.Queues) > 0 {
		queues = make(map[string]river.QueueConfig, len(cfg.Queues))
		for name, n := range cfg.Queues {
			if name == "" || n < 1 {
				return nil, fmt.Errorf("queue: queue %q needs a name and a positive concurrency", name)
			}
			queues[name] = river.QueueConfig{MaxWorkers: n}
		}
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:       cfg.Logger,
		PeriodicJobs: cfg.PeriodicJobs,
		Queues:       queues,
		Workers:      cfg.Workers,
	})
	if err != nil {
		return nil, fmt.Errorf("queue: worker client: %w", err)
	}
	return client, nil
}

// MigrateUp applies River's migrations.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("queue: migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("queue: migrate up: %w", err)
	}
	return nil
}

// MigrateDown removes River's schema.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("queue: migrator: %w", err)
	}
	opts := &rivermigrate.MigrateOpts{MaxSteps: migrateDownMaxSteps}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionDown, opts); err != nil {
		return fmt.Errorf("queue: migrate down: %w", err)
	}
	return nil
}
