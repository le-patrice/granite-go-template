package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"example.com/app/internal/config"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/idempotency"
	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/store"
	"example.com/app/internal/platform/webhook"

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
	workerReadyTimeout      = 2 * time.Second
	workerHealthStopTimeout = 5 * time.Second
	workerHealthHeaderLimit = 5 * time.Second
)

// RunWorker runs the River worker and the idempotency-key cleanup loop until
// ctx is cancelled, then stops fetching, drains running jobs, and only then
// closes the pool. River's Start context is deliberately not the signal
// context: cancelling it would hard-stop jobs, so shutdown goes through Stop.
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	workerCfg := queue.WorkerConfig{
		Concurrency: cfg.WorkerConcurrency,
		Queues:      cfg.WorkerQueues,
		Logger:      logger,
	}
	pool, err := store.NewPool(ctx, store.PoolConfig{
		URL:             cfg.DirectDatabaseURL,
		MaxConns:        int32(workerCfg.TotalWorkers()) + workerConnHeadroom,
		MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	defer pool.Close()

	workers := queue.NewWorkers()

	processors := map[string]webhook.Processor{}
	// template:example:begin acme
	processors["acme"] = webhook.LogProcessor(logger)
	// template:example:end acme
	webhook.RegisterWorker(workers, pool, processors)

	// template:example:begin orders
	orders.RegisterWorkers(workers, orders.NewRepo(pool, nil), orders.NewLogNotifier(logger))
	// template:example:end orders

	workerCfg.Workers = workers
	client, err := queue.NewWorkerClient(pool, workerCfg)
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
	stopHealth := startWorkerHealth(ctx, cfg.WorkerHealthAddr, pool, logger)
	defer stopHealth()
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

// startWorkerHealth serves /healthz and /readyz so orchestrators can probe the
// worker. It returns a stop function that shuts the listener down and waits for
// it. An empty addr disables the listener.
func startWorkerHealth(ctx context.Context, addr string, pool store.Pinging, logger *slog.Logger) func() {
	if addr == "" {
		return func() {}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpx.Healthz())
	mux.Handle("GET /readyz", httpx.Readyz(logger, workerReadyTimeout, store.Pinger(pool)))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: workerHealthHeaderLimit,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "worker health listener failed", "error", err)
		}
	}()
	return func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workerHealthStopTimeout)
		defer cancel()
		if err := srv.Shutdown(stopCtx); err != nil {
			logger.ErrorContext(stopCtx, "worker health shutdown", "error", err)
		}
		<-done
	}
}
