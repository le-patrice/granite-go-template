# Template build ledger

This file tracks how the template repository was generated, phase by phase.
It is template-only: `make init` removes it from scaffolded projects.

Status values: pending, pass, fail.

## Adoption levels

| Level | Name | Phases |
|-------|------|--------|
| 1 | Foundation | 0-3 |
| 2 | Identity | 4 |
| 3 | Reliability | 5-7 |
| 4 | Operations | 8-10 |

## Phases

| Phase | Name | Status | Gate result | Notes |
|-------|------|--------|-------------|-------|
| 0 | Setup | pass | all tools verified (go 1.27.1, golangci-lint v2.14.0, sqlc v1.31.1, goose v3.29.0, govulncheck, docker 29.7.2, compose v5.4.0, k6 v1.4.0) | Level 1 (Foundation) |
| 1 | Skeleton | pass | go build, go vet, golangci-lint config verify, make lint (0 issues), make arch-check (ok) | Adds `internal/platform/doc.go` as a lint anchor |
| 2.a | Config, telemetry, app/api.go, cmd/api | pass | go vet, make lint (0 issues), make test, fail-fast env check, live API probe | |
| 2.b | httpx | pass | go vet, make lint (0 issues), make arch-check (ok), make test, /healthz, /readyz, /metrics | |
| 3.a | Core migrations, queries, sqlc | pass | make sqlc, sqlc diff (clean), migrate-verify (up/down/up v4), make migrate | |
| 3.b | Store pool, tx, storetest | pass | make lint (0 issues), arch-check (ok), make test, make test-integration, /readyz 200->503->200 | Level 1 (Foundation) complete |
| 4.a | platform/auth | pass | go vet, make lint (0 issues), make test (argon2id, paseto v4.local, RBAC) | |
| 4.b | ratelimit, users, cmd/seed | pass | make lint (0 issues), depguard rules proven, make test, make test-integration, seed-admin, live auth & 429 probes | Level 2 (Identity) complete |
| 5.a | platform/queue, orders example | pass | make sqlc, sqlc diff (clean), make lint (0 issues), arch-check (ok), make test | |
| 5.b | Jobs, app/worker.go, cmd/worker | pass | migrate-verify (goose + river up/down/up), make test-integration, live order + worker outbox probe | |
| 6 | Guards | pass | make lint (0 issues), arch-check (ok), make test, make test-integration, live idempotent order & 429 probes | |
| 7.a | platform/webhook | pass | make lint (0 issues), arch-check (ok), make test, make test-integration | |
| 7.b | platform/outbound, integrations/acme | pass | make lint (0 issues), arch-check (ok), make test, make test-integration, live webhook + worker probe | Level 3 (Reliability) complete |
| 8.a | Deployments, perf, templates | pass | distroless Dockerfile builds, compose up/down verified, k6 smoke script, template stubs | |
| 8.b | Scaffolding & add-ons transition | pass | Transitioning init/strip/diff to gonew-compatible cmd/scaffold add-on | See docs/ADDONS.md |
| 9 | Guidance, documentation, and core ADRs | pass | AGENTS.md, CLAUDE.md, README.md, architecture/porting/upgrading guides, ADRs 0001-0004 | |
| 10 | Final verification | pass | make lint, arch-check, test, docker build all green | Proceeding to Add-ons |

## Decisions

Each line here becomes an ADR in Phase 9.

1. Template repo uses placeholder tokens `{{APP_NAME}}` and `{{MODULE_PATH}}` in documentation; Go files use the valid stand-in module path `example.com/app` that `scripts/init.sh` rewrites.
2. Chat protocol: one phase per reply, gate verified before advancing.
3. `go.mod` declares `go 1.22` as the minimum.
4. `init.sh` replaces only the exact tokens `{{APP_NAME}}` and `{{MODULE_PATH}}`, so GitHub `${{ ... }}` expressions are untouched.
5. `internal/platform/doc.go` is included in Phase 1 (template-owned) so `golangci-lint` and `arch-check` have a Go package to analyze before Phase 2.
6. The `depguard` `domain-purity` rule uses a glob over `internal/*/*.go` with exclusions (`handler.go`, `repo.go`, `jobs.go`, `app`, `config`, `platform`, `$test`), verified against `golangci-lint` v2.14.0.
7. `forbidigo` enforces no `fmt.Print*` and no `log` package, and `gochecknoinits` enforces no `init()`.
8. The `Makefile` loads `.env` per recipe through `LOADENV` instead of `include`, so quoted values do not leak into make variables. `migrate` falls back from `DIRECT_DATABASE_URL` to `DATABASE_URL`.
9. `migrate-verify` runs against local Postgres defaults (user `app`, password `app`, `localhost:5432`) that `deployments/compose.yaml` matches in Phase 8.a. CI overrides `PSQL`.
10. River migrations join `make migrate` in Phase 5, since Phase 1 has no River dependency.
11. `ci.yml` will be extended in Phase 8.b to add `scaffold-smoke`.
12. The k6 entry script will be `perf/smoke.js`.
13. No `.gitkeep` files, because later phases populate the directories.
14. The `{{APP_NAME}}` token may also appear inside Go string literals (the `SERVICE_NAME` default). `init.sh` rewrites it in every file type.
15. The config package parses with `caarlos0/env`, then runs a separate validator that joins every invalid field with `errors.Join`.
16. Config exposes `LogValue` (slog redaction) and `TokenKey()`. `DATABASE_URL`, the token key, and the webhook secret are never logged.
17. `WEBHOOK_ACME_SECRET` sits inside `template:example` markers in both `config.go` and `.env.example`.
18. No OpenTelemetry globals are set. `telemetry.Setup` returns a `Provider` that is passed explicitly to the middleware. With no endpoint it is a no-op that still propagates incoming trace context.
19. Only traces go through OTLP. Metrics are served by Prometheus on `/metrics` from a private registry (no global registry).
20. `Router` wraps `ServeMux` and records the matched pattern in a per-request holder so route labels work on Go 1.22+ and raw paths never leak into metrics, logs, or span names.
21. Middleware order is Recover, RequestID, RealIP, Telemetry, Metrics.
22. Telemetry and Metrics recover a panic only to record it as a 500, then re-raise it for Recover to answer.
23. Default `ServeMux` 404 and 405 responses remain plain text so 405 Method Not Allowed is not masked by a catch-all route.
24. `http.Server.ErrorLog` routes through slog via `slog.NewLogLogger`, so the `log` package is never imported directly.
25. In HTTP test files, `httptest.NewRequestWithContext` is used instead of `httptest.NewRequest` to satisfy `noctx` in `golangci-lint` v2.14.0, and `ctx := r.Context()` is bound outside `defer` closures in `Recover` and `Metrics.Middleware` to satisfy `contextcheck`.
26. Migrations 1-4 are plain goose SQL with working `Down` sections. PostgreSQL 13 or newer is required because `gen_random_uuid()` is built in, so no `CREATE EXTENSION` is needed.
27. `refresh_tokens.user_id` cascades on user delete, and `family_id` and `user_id` are indexed. `idempotency_keys` and `refresh_tokens` carry an extra index on their time column to make cleanup jobs cheap.
28. `idempotency_keys` keeps the specified columns. A row whose `status` is NULL means "in flight" (the 409 case), and replays are served from `status` and `response_body`.
29. Queries are written with `sqlc.arg(name)` so generated parameter structs are named. `uuid` maps to `google/uuid.UUID` and `timestamptz` to `time.Time` (`*time.Time` when nullable), so repos do not leak `pgtype` into domain code.
30. `InsertInboundEvent` uses `ON CONFLICT DO NOTHING RETURNING id`. A duplicate yields `pgx.ErrNoRows`, which the Phase 7 webhook handler maps to "200 without enqueue".
31. The pool is created lazily. The API starts while the database is down and `/readyz` reports 503 until it recovers.
32. PgBouncer decision: behind PgBouncer in transaction mode, set `default_query_exec_mode=cache_describe` (or `exec`) as a parameter in `DATABASE_URL`. River needs `DIRECT_DATABASE_URL` or its poll-only setting.
33. `store.InTx` wraps `pgx.BeginFunc` and `InTxOptions` wraps `pgx.BeginTxFunc`. Business writes and River `InsertTx` share one transaction through them.
34. `platform/store/errors.go` holds `UniqueViolation` and `ForeignKeyViolation` so repos translate driver errors without re-implementing `pgconn` unwrapping.
35. `storetest` is built only with the `integration` tag (its `doc.go` carries no tag so the package exists in normal builds), so production builds never pull in testcontainers. It uses the goose Provider API instead of goose's global dialect state.
36. `storetest.WithMigration` lets Phase 5 apply River's migrations without editing `storetest`.
37. Test files may hold one package-level `testDB` handle assigned in `TestMain`. This is the only exception to "no package-level mutable state", and applies to `_test.go` files only.
38. `goleak` is not used in `storetest` packages because the Docker client and container reaper start goroutines that would be reported as leaks. It remains in `httpx`.
39. `internal/app/api.go` is extended with the pool, the `/readyz` DB check, and `pool.Close()` after the server drains. Deferred calls close the pool before flushing telemetry.
40. `go mod tidy` after `make sqlc` is required so tidy sees the generated imports.
41. Passwords: argon2id with PHC strings (`$argon2id$v=19$m=..,t=..,p=..$salt$hash`), 16-byte random salt, 32-byte output, `subtle.ConstantTimeCompare`. Verification uses the parameters embedded in the stored hash, and `NeedsRehash` compares them with the current config.
42. Passwords must be 12 to 1024 characters (minimum counted in runes, cap in bytes). `users` and `auth` both enforce it.
43. The unknown-user path calls `VerifyDummy` against a precomputed hash. Unknown user, wrong password, and disabled account all return `ErrInvalidCredentials`.
44. Access tokens are PASETO v4.local with subject, role, issued-at, not-before (`SetNotBefore(issuedAt)` required by `paseto.ValidAt`), expiry, and a random token ID. Every parse failure returns `ErrInvalidToken`.
45. Refresh tokens are 32 random bytes in base64url; only the SHA-256 hash is stored. Rotation locks the row (`FOR UPDATE`), applies `RefreshToken.Verdict`, marks the token rotated, and inserts its successor in the same family in one transaction. Reusing a rotated token revokes the family and commits that revocation.
46. Refresh for a disabled or deleted user revokes the family and returns `ErrInvalidToken`.
47. `/v1/auth/logout` takes the refresh token in the body, needs no access token, and is idempotent.
48. Domain files `users.go` and `service.go` import only stdlib and `google/uuid`.
49. `ratelimit.ByClientIP` uses `httpx.ClientIPFrom` when present and strips the port with `net.SplitHostPort` when falling back to `r.RemoteAddr`.
50. The limiter starts no goroutines, sweeping idle keys lazily from `Allow` and bounding memory with `MaxKeys`.
51. `internal/app/seed.go` is the composition root for `cmd/seed`, using `DIRECT_DATABASE_URL` and reading `SEED_ADMIN_EMAIL` and `SEED_ADMIN_PASSWORD`.
52. Both `depguard` rules (`domain-purity` and `features-no-vendors`) were proven with temporary violation files in the Phase 4 gate.
53. River's migrations run through `cmd/worker migrate -direction up|down [-url ...]`, using the `rivermigrate` library pinned in `go.mod`. `make migrate` and `make migrate-verify` invoke it, and `storetest` applies it via `WithMigration(queue.MigrateUp)`.
54. `platform/queue` provides an insert-only client for the API and a worker client for `cmd/worker`. The worker uses `DIRECT_DATABASE_URL` because River uses `LISTEN/NOTIFY`.
55. `orders` writes the order and enqueues its `order.created` River job in one transaction (`store.InTx` + `jobs.InsertTx`). `orders/service.go` imports neither `pgx` nor `river`.
56. Jobs are at-least-once; `OrderCreatedWorker` cancels the job (`river.JobCancel`) if the order no longer exists and returns errors otherwise so River retries.
57. Ownership is enforced in `orders/service.go`: members read only their own orders (404 on others' orders), admins read all.
58. Orders list uses keyset pagination (`WHERE ... id < cursor ORDER BY id DESC LIMIT n`, never `OFFSET`), fetching `size+1` rows to compute `NextCursor`.
59. `RunWorker` starts River with `context.WithoutCancel(ctx)` and shuts down via `client.Stop` (20s drain) with fallback to `StopAndCancel` before closing the pool.
60. Example wiring in `internal/app/api.go` and `internal/app/worker.go` is wrapped in `// template:example:begin orders` and `// template:example:end orders` markers.
61. The sqlc-backed idempotency store lives in `internal/app`, not `platform/idempotency`, because `arch-check` forbids `platform` importing `internal/store/sqlc`.
62. The request hash is SHA-256 over method, request URI, and body. Reusing a key on another endpoint or with another body returns 422. Scope is supplied by a `Scope` function so `platform/idempotency` does not import `platform/auth`.
63. Responses with status >= 500, responses larger than `MaxBodyBytes`, and panics release the key so the client can retry. Stored responses keep a JSON envelope with `Content-Type` and `Location` headers and replays set `Idempotent-Replayed: true`.
64. Concurrent duplicates return 409 with `Retry-After: 1`. Abandoned in-flight rows are deleted by `idempotency.Cleanup`, which the worker runs hourly with a 24h TTL.
65. The general per-IP limiter (`RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`) is the last step of the global middleware chain (after metrics) so 429s are counted and carry a request ID.
66. `newChain` and `chainDeps` were extracted in `internal/app/api.go` so the chain order and limiter behavior are unit-tested without starting a server.
67. Files that are wholly an example outside `internal/orders/` carry `// template:example:file orders` so `strip-examples` deletes them (`internal/app/idempotency_orders_integration_test.go`).
68. `webhook.Ingest` checks in order: unknown source (404), body size (413), signature (403), JSON and event ID (400), persistence (503). No database call happens before signature verification.
69. Each webhook source supplies a `Verifier` (constant-time `hmac.Equal`) and an `ExternalID` extractor. Duplicate `(source, external_id)` hits `ON CONFLICT DO NOTHING`, stores and enqueues nothing, and returns 200.
70. The event row and its `webhook.event` job are written in one transaction with plain SQL through `store.InTx`. If enqueue fails, the event rolls back and returns 503 so the sender retries.
71. `webhook.Worker` loads the stored event and dispatches to the `Processor` registered for its source. Missing events or sources with no processor cancel the job (`river.JobCancel`).
72. The webhook worker is registered as core wiring, so `cmd/worker` always has at least one River worker even after `strip-examples`.
73. `platform/outbound` applies per-attempt timeouts from the caller's context, releases the timeout on response body `Close`, and retries idempotent methods (or requests with `Idempotency-Key`) with full-jitter exponential backoff when the body is replayable.
74. Retried outcomes are transport errors, 429, 502, 503, and 504. `Retry-After` (seconds or HTTP date) is honored on 429 and 503 up to `MaxRetryAfter`; 500 is not retried.
75. The circuit breaker counts consecutive failures (transport errors and status >= 500, not 429), opens for `OpenFor`, and allows one half-open probe. Caller-cancelled attempts do not count as failures.
76. `integrations/acme` verifies `X-Acme-Signature: sha256=<hex HMAC-SHA256>` with a minimum 16-char secret and provides a reference outbound client built on `platform/outbound`.
77. Acme wiring in `internal/app` sits inside `// template:example:begin acme` and `// template:example:end acme` markers.
