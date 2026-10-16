# Architecture

## Dependency Direction

All dependencies point inward:

```text
cmd/{api,worker,seed}
        │
        ▼
   internal/app              (composition roots, graceful shutdown, sqlc idempotency adapter)
     │       │
     ▼       ▼
features   integrations      (internal/users, internal/orders; internal/integrations/acme)
     │       │
     ▼       ▼
 internal/platform/*         (httpx, store, queue, idempotency, ratelimit, auth, webhook, outbound, telemetry)
```

### Enforced Boundaries

1. **`internal/platform/*` imports nothing outside `platform/`:** Verified by `scripts/arch-check.sh`. Platform packages accept plain configuration values, never `internal/config`, `internal/store/sqlc`, features, or integrations.
2. **Domain purity (`<feature>.go` and `service.go`):** Enforced by `depguard` (`domain-purity`). Domain entities and rules import only the Go standard library and `github.com/google/uuid`. Neither `pgx`, `river`, nor `net/http` may be imported in `<feature>.go` or `service.go`.
3. **No vendor leakage (`features-no-vendors`):** Enforced by `depguard`. Only `internal/app` and `internal/integrations/*` may import `internal/integrations/*`. Features never import each other or vendor packages.
4. **Huma boundary (`huma-boundary`):** Enforced by `depguard`. Features never import `github.com/danielgtaylor/huma/v2` directly; all typed operation primitives, types, and registration helpers are re-exported through `internal/platform/httpx`.

## Feature Anatomy

Each business capability under `internal/<feature>/` follows a 5-file structure:

- `<feature>.go`: Domain entities, value validation, invariants, and domain sentinel errors.
- `service.go`: Application use-cases and small consumer-defined repository/notifier interfaces.
- `repo.go`: Implements the service's interfaces using `internal/store/sqlc` and `internal/platform/store.InTx`; translates driver errors (`pgx.ErrNoRows`, unique violations) into domain errors.
- `handler.go`: HTTP transport adapter; registers typed Huma operations via `httpx.Register` and maps domain errors to RFC 9457 `application/problem+json` responses.
- `jobs.go`: River job arguments and idempotent background workers (present when the feature has async work).

## Request Lifecycle & Middleware Split

The HTTP stack is split into global router middleware and route-level operation middleware:

### Global Chain (`internal/app/api.go`)
Executes on all requests via `httpx.Chain` before route matching:
1. `httpx.Recover`: Catches panics, logs stack traces with `request_id`, and returns a generic 500 RFC 9457 problem response.
2. `httpx.RequestID`: Propagates or generates `X-Request-ID`.
3. `httpx.RealIP`: Extracts the client IP from `CF-Connecting-IP` or `X-Forwarded-For` only when the immediate peer is inside `TRUSTED_PROXIES`.
4. `httpx.Telemetry`: Starts an OpenTelemetry span named after the matched `ServeMux` pattern and emits structured JSON access logs via `log/slog`.
5. `httpx.Metrics`: Records request count and duration histograms labeled by route pattern (`GET /v1/orders/{id}`), never raw paths.
6. `ratelimit.Middleware`: Enforces the general per-IP token bucket (`RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`) after metrics so 429s are counted and carry `X-Request-ID`.

### Operation Middleware (`httpx.AdaptMiddleware`)
Attached per operation on Huma routes:
- `auth.RequireAuth`: Validates PASETO bearer tokens and injects `Principal` into context.
- Stricter rate limiting on `/v1/auth/*`.
- `idempotency.Middleware`: Enforces header validation, response buffering, and replay on mutating endpoints (`POST /v1/orders`).

## Code-First OpenAPI & Route Coverage

OpenAPI 3.1 specification generation is derived code-first from typed operation structs:
- `cmd/openapi`: Pure-Go CLI to export OpenAPI 3.1 (default) or 3.0.3 (`-version 3.0`) YAML specs without starting the server.
- `make openapi`: Updates `api/openapi.yaml`.
- `make openapi-check`: Verifies determinism (two runs match) and asserts zero drift against `api/openapi.yaml` in CI.
- `internal/app/routes_test.go`: Automated route coverage test asserting every route registered on `ServeMux` is present in the OpenAPI specification or explicitly exempted with a reason.

## Transactional Outbox & Webhooks

- **Outbound jobs (`orders`):** Business state and River jobs are written in a single PostgreSQL transaction using `store.InTx` and `jobs.InsertTx`. If either fails, both roll back.
- **Inbound webhooks (`platform/webhook`):** `POST /v1/webhooks/{source}` caps the body, verifies the HMAC signature in constant time (`hmac.Equal`) before touching the database, and then in one transaction inserts into `inbound_events` (`ON CONFLICT (source, external_id) DO NOTHING`) and enqueues a `webhook.event` River job.
