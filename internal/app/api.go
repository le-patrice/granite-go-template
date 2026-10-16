// Package app holds the composition roots, one per binary. It is the only
// place that wires platform packages, features, and integrations together.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"example.com/app/internal/config"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/store"
	"example.com/app/internal/platform/telemetry"
)

const (
	shutdownTimeout  = 20 * time.Second
	telemetryTimeout = 5 * time.Second
	readyTimeout     = time.Second
)

// RunAPI builds the HTTP stack and serves until ctx is cancelled, then drains
// in-flight requests, closes the database pool, and flushes telemetry.
func RunAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	prov, err := telemetry.Setup(ctx, telemetry.Options{
		ServiceName: cfg.ServiceName,
		Endpoint:    cfg.OtelEndpoint,
	})
	if err != nil {
		return fmt.Errorf("app: telemetry: %w", err)
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telemetryTimeout)
		defer cancel()
		if err := prov.Shutdown(flushCtx); err != nil {
			logger.ErrorContext(flushCtx, "telemetry shutdown failed", "error", err)
		}
	}()

	pool, err := store.NewPool(ctx, store.PoolConfig{URL: cfg.DatabaseURL})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	defer pool.Close()

	proxies, err := httpx.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	metrics, err := httpx.NewMetrics()
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	router := httpx.NewRouter()
	router.Handle("GET /healthz", httpx.Healthz())
	router.Handle("GET /readyz", httpx.Readyz(logger, readyTimeout, store.Pinger(pool)))
	router.Handle("GET /metrics", metrics.Handler())

	handler := httpx.Chain(router,
		httpx.Recover(logger),
		httpx.RequestID(),
		httpx.RealIP(proxies),
		httpx.Telemetry(logger, prov.TracerProvider, prov.Propagator),
		metrics.Middleware(),
	)

	srv := httpx.NewServer(httpx.ServerConfig{
		Addr:    cfg.Addr,
		Handler: handler,
		Logger:  logger,
	})

	logger.InfoContext(ctx, "api listening", "addr", cfg.Addr)
	if err := httpx.Run(ctx, srv, shutdownTimeout); err != nil {
		return fmt.Errorf("app: run api: %w", err)
	}
	logger.InfoContext(ctx, "api stopped")
	return nil
}
