// Package app holds the composition roots, one per binary. It is the only
// place that wires platform packages, features, and integrations together.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/app/internal/config"
	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/ratelimit"
	"example.com/app/internal/platform/store"
	"example.com/app/internal/platform/telemetry"
	"example.com/app/internal/users"

	// template:example:begin orders
	"example.com/app/internal/orders"
	"example.com/app/internal/platform/queue"
	// template:example:end orders
)

const (
	shutdownTimeout  = 20 * time.Second
	telemetryTimeout = 5 * time.Second
	readyTimeout     = time.Second
	limiterIdleTTL   = 10 * time.Minute
	limiterMaxKeys   = 100_000
)

// identity bundles the pieces every binary needs to work with accounts.
type identity struct {
	users  *users.Service
	tokens *auth.TokenManager
}

func newIdentity(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (identity, error) {
	hasher, err := auth.NewHasher(auth.PasswordParams{
		MemoryKiB: cfg.Argon2MemoryKiB,
		Time:      cfg.Argon2Time,
		Threads:   cfg.Argon2Threads,
	})
	if err != nil {
		return identity{}, err
	}
	key, err := cfg.TokenKey()
	if err != nil {
		return identity{}, err
	}
	tokens, err := auth.NewTokenManager(auth.TokenConfig{Key: key, AccessTTL: cfg.AccessTokenTTL})
	if err != nil {
		return identity{}, err
	}
	svc := users.NewService(users.NewRepo(pool), hasher, tokens, users.Options{
		RefreshTTL: cfg.RefreshTokenTTL,
		Logger:     logger,
	})
	return identity{users: svc, tokens: tokens}, nil
}

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
	ids, err := newIdentity(cfg, pool, logger)
	if err != nil {
		return fmt.Errorf("app: identity: %w", err)
	}
	authLimiter, err := ratelimit.New(ratelimit.Config{
		Rate:    ratelimit.PerMinute(cfg.AuthRateLimitPerMin),
		Burst:   cfg.AuthRateLimitPerMin,
		IdleTTL: limiterIdleTTL,
		MaxKeys: limiterMaxKeys,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	router := httpx.NewRouter()
	router.Handle("GET /healthz", httpx.Healthz())
	router.Handle("GET /readyz", httpx.Readyz(logger, readyTimeout, store.Pinger(pool)))
	router.Handle("GET /metrics", metrics.Handler())

	users.NewHandler(ids.users, logger).Routes(
		router,
		ratelimit.Middleware(authLimiter, ratelimit.ByClientIP),
		auth.RequireAuth(ids.tokens),
	)

	// template:example:begin orders
	jobs, err := queue.NewInsertClient(pool)
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	orders.NewHandler(orders.NewService(orders.NewRepo(pool, jobs)), logger).Routes(
		router,
		auth.RequireAuth(ids.tokens),
		nil,
	)
	// template:example:end orders

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
