package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"example.com/app/internal/config"
	"example.com/app/internal/platform/idempotency"
	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/store"

	// template:example:begin orders
	"example.com/app/internal/orders"
	// template:example:end orders
)

const (
	workerStopTimeout       = 20 * time.Second
	workerCancelTimeout     = 5 * time.Second
	workerConnHeadroom      = 4
	idempotencyCleanupEvery = time.Hour
	idempotencyKeyTTL       = 24 * time.Hour
)

// RunWorker runs the River worker and the idempotency-key cleanup loop until
// ctx is cancelled, then stops fetching, drains running jobs, and only then
// closes the pool. River's Start context is deliberately not the signal
// context: cancelling it would hard-stop jobs, so shutdown goes through Stop.
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := store.NewPool(ctx, store.PoolConfig{
		URL:      cfg.DirectDatabaseURL,
		MaxConns: int32(cfg.WorkerConcurrency) + workerConnHeadroom,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	defer pool.Close()

	workers := queue.NewWorkers()
	// template:example:begin orders
	orders.RegisterWorkers(workers, orders.NewRepo(pool, nil), orders.NewLogNotifier(logger))
	// template:example:end orders

	client, err := queue.NewWorkerClient(pool, queue.WorkerConfig{
		Workers:     workers,
		Concurrency: cfg.WorkerConcurrency,
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	var cleanup sync.WaitGroup
	cleanupCtx, stopCleanup := context.WithCancel(context.WithoutCancel(ctx))
	cleanup.Add(1)
	go func() {
		defer cleanup.Done()
		idempotency.Cleanup(cleanupCtx, newIdempotencyStore(pool), idempotencyCleanupEvery, idempotencyKeyTTL, logger)
	}()
	defer func() {
		stopCleanup()
		cleanup.Wait()
	}()

	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("app: start worker: %w", err)
	}
	logger.InfoContext(ctx, "worker started", "concurrency", cfg.WorkerConcurrency)

	<-ctx.Done()

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workerStopTimeout)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		logger.ErrorContext(stopCtx, "worker did not drain in time; cancelling running jobs", "error", err)
		cancelCtx, cancelCancel := context.WithTimeout(context.WithoutCancel(ctx), workerCancelTimeout)
		defer cancelCancel()
		if err := client.StopAndCancel(cancelCtx); err != nil {
			return fmt.Errorf("app: stop worker: %w", err)
		}
	}
	logger.InfoContext(ctx, "worker stopped")
	return nil
}

// RunMigrate applies or removes River's migrations against url.
func RunMigrate(ctx context.Context, url, direction string, logger *slog.Logger) error {
	pool, err := store.NewPool(ctx, store.PoolConfig{URL: url})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	defer pool.Close()

	switch direction {
	case "up":
		err = queue.MigrateUp(ctx, pool)
	case "down":
		err = queue.MigrateDown(ctx, pool)
	default:
		return fmt.Errorf("app: unknown migration direction %q (want up or down)", direction)
	}
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	logger.InfoContext(ctx, "river migrations applied", "direction", direction)
	return nil
}
