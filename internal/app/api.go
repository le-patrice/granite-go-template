// Package app holds the composition roots, one per binary. It is the only
// place that wires platform packages, features, and integrations together.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"example.com/app/internal/config"
	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
	"example.com/app/internal/platform/queue"
	"example.com/app/internal/platform/ratelimit"
	"example.com/app/internal/platform/store"
	"example.com/app/internal/platform/telemetry"
	"example.com/app/internal/platform/webhook"
	"example.com/app/internal/users"

	// template:example:begin acme
	"example.com/app/internal/integrations/acme"
	// template:example:end acme
	// template:example:begin orders
	"example.com/app/internal/orders"
	"example.com/app/internal/platform/idempotency"
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

// chainDeps are the inputs of the global middleware chain.
type chainDeps struct {
	logger  *slog.Logger
	proxies httpx.TrustedProxies
	prov    *telemetry.Provider
	metrics *httpx.Metrics
	limiter *ratelimit.Limiter
}

// newChain wraps the router in the global middleware, in the specified order:
// recover, request ID, real IP, telemetry and log, metrics, rate limit.
func newChain(router http.Handler, d chainDeps) http.Handler {
	return httpx.Chain(router,
		httpx.Recover(d.logger),
		httpx.RequestID(),
		httpx.RealIP(d.proxies),
		httpx.Telemetry(d.logger, d.prov.TracerProvider, d.prov.Propagator),
		d.metrics.Middleware(),
		ratelimit.Middleware(d.limiter, ratelimit.ByClientIP),
	)
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
	jobs, err := queue.NewInsertClient(pool)
	if err != nil {
		return fmt.Errorf("app: %w", err)
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
	generalLimiter, err := ratelimit.New(ratelimit.Config{
		Rate:    rate.Limit(cfg.RateLimitRPS),
		Burst:   cfg.RateLimitBurst,
		IdleTTL: limiterIdleTTL,
		MaxKeys: limiterMaxKeys,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	sources := map[string]webhook.Source{}
	// template:example:begin acme
	acmeSource, err := acme.WebhookSource(cfg.WebhookAcmeSecret)
	if err != nil {
		return fmt.Errorf("app: acme webhook: %w", err)
	}
	sources["acme"] = acmeSource
	// template:example:end acme
	ingest, err := webhook.Ingest(webhook.Config{
		DB:      pool,
		Jobs:    jobs,
		Sources: sources,
		Logger:  logger,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	router := httpx.NewRouter()
	router.Handle("GET /healthz", httpx.Healthz())
	router.Handle("GET /readyz", httpx.Readyz(logger, readyTimeout, store.Pinger(pool)))
	router.Handle("GET /metrics", metrics.Handler())
	router.Handle("POST /v1/webhooks/{source}", ingest)

	api := httpx.NewAPI(router, httpx.APIConfig{
		Title:         cfg.ServiceName,
		Version:       cfg.AppVersion,
		DocsEnabled:   cfg.DocsEnabled,
		PublicBaseURL: cfg.PublicBaseURL,
	})
	RegisterDocumentedRoutes(api)

	users.NewHandler(ids.users, logger).Routes(
		api,
		ratelimit.Middleware(authLimiter, ratelimit.ByClientIP),
		auth.RequireAuth(ids.tokens),
	)

	// template:example:begin orders
	idempotent, err := idempotency.Middleware(idempotency.Config{
		Store:  newIdempotencyStore(pool),
		Scope:  principalScope,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	orders.NewHandler(orders.NewService(orders.NewRepo(pool, jobs)), logger).Routes(
		api,
		auth.RequireAuth(ids.tokens),
		idempotent,
	)
	// template:example:end orders

	handler := newChain(router, chainDeps{
		logger:  logger,
		proxies: proxies,
		prov:    prov,
		metrics: metrics,
		limiter: generalLimiter,
	})

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

// RegisterDocumentedRoutes registers non-Huma operational endpoints directly in the OpenAPI spec.
func RegisterDocumentedRoutes(api httpx.API) {
	httpx.AddDocumentedRoute(api, &httpx.Operation{
		OperationID: "healthCheck",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness probe",
		Description: "Returns 200 OK when the process is alive.",
		Tags:        []string{"Operations"},
		Responses: map[string]*httpx.Response{
			"200": {
				Description: "Service is alive",
			},
		},
	})
	httpx.AddDocumentedRoute(api, &httpx.Operation{
		OperationID: "readinessCheck",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Readiness probe",
		Description: "Checks database connectivity and readiness to serve traffic.",
		Tags:        []string{"Operations"},
		Responses: map[string]*httpx.Response{
			"200": {
				Description: "Service is ready to accept traffic",
			},
			"503": {
				Description: "Service is not ready",
			},
		},
	})
	httpx.AddDocumentedRoute(api, &httpx.Operation{
		OperationID: "prometheusMetrics",
		Method:      http.MethodGet,
		Path:        "/metrics",
		Summary:     "Prometheus metrics",
		Description: "Scrape endpoint for Prometheus format metrics.",
		Tags:        []string{"Operations"},
		Responses: map[string]*httpx.Response{
			"200": {
				Description: "Prometheus metrics scrape response",
				Content: map[string]*httpx.MediaType{
					"text/plain": {},
				},
			},
		},
	})
	httpx.AddDocumentedRoute(api, &httpx.Operation{
		OperationID: "ingestWebhook",
		Method:      http.MethodPost,
		Path:        "/v1/webhooks/{source}",
		Summary:     "Inbound webhook ingest",
		Description: "Accepts signed inbound webhooks from external providers (e.g. Acme) and schedules background processing.",
		Tags:        []string{"Webhooks"},
		Parameters: []*httpx.Param{
			{
				Name:        "source",
				In:          "path",
				Required:    true,
				Description: "Webhook provider source name (e.g. acme)",
				Schema:      &httpx.Schema{Type: "string"},
			},
		},
		Responses: map[string]*httpx.Response{
			"200": {
				Description: "Webhook received and scheduled",
			},
			"400": {
				Description: "Invalid payload or signature header missing",
			},
			"401": {
				Description: "Invalid webhook signature",
			},
		},
	})
}

// template:example:begin orders
// principalScope scopes idempotency keys to the authenticated user.
func principalScope(r *http.Request) string {
	if principal, ok := auth.PrincipalFrom(r.Context()); ok {
		return principal.UserID.String()
	}
	return ""
}

// template:example:end orders
