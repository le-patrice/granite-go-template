# granite-go ({{APP_NAME}})

> Enterprise-grade Go service template. A modular monolith with a transactional outbox, typed
> OpenAPI, and production deployment defaults. Built to be scaffolded into many projects.

`net/http` (Go 1.22 `ServeMux`) · `pgx/v5` + `sqlc` · River (jobs and outbox) · Huma v2 (OpenAPI 3.1) ·
PASETO v4 + argon2id · OpenTelemetry · Prometheus · Kustomize

[Docs](docs/README.md) · [Architecture](docs/ARCHITECTURE.md) · [Domain Blueprint](docs/DOMAIN_BLUEPRINT.md) · [Operations](docs/OPERATIONS.md) · [Scaling](docs/SCALING.md) · [AGENTS.md](AGENTS.md)

---

## Why this template

| Principle | What it means here |
|---|---|
| Domain isolated from infrastructure | Feature domain files import only the stdlib and `uuid`. `depguard` and `arch-check` enforce it in CI. |
| No dual writes | A business change and the event it emits commit in **one** Postgres transaction (`pgx.BeginFunc` + River `InsertTx`). |
| Retry-safe by default | Idempotency keys on writes, deduplicated signed webhooks, at-least-once workers that are idempotent. |
| Contract first | Typed Huma operations generate `api/openapi.yaml`. CI fails on drift. Errors are RFC 9457 problem+json. |
| Small dependency surface | One allowlist ([ADR 0001](docs/adr/0001-stack.md)). No ORM, web framework, DI container or alternative logger. |
| Operable from day one | Health probes for API and worker, metrics, traces, alert rules, runbooks, Kubernetes manifests, image scanning. |

## Architecture at a glance

```text
cmd/{api,worker,seed}          entrypoints only
        │
   internal/app                composition roots, graceful shutdown
     │         │
 features   integrations       internal/<feature>   internal/integrations/<vendor>
     │         │
 internal/platform/*           httpx store queue idempotency ratelimit auth webhook outbound telemetry
```

Each feature is five files: `<feature>.go` (rules), `service.go` (use cases and the interfaces
it needs), `repo.go` (sqlc), `handler.go` (HTTP), `jobs.go` (async). A service is unit-testable
with a fake repository: no database, no HTTP. Details in [Architecture](docs/ARCHITECTURE.md).

## Quick start

```bash
make init NAME=billing-api MODULE=github.com/acme/billing-api EXAMPLES=keep   # once, after cloning
cp .env.example .env
make up                 # local Postgres (Docker or Podman, auto-detected)
make migrate            # app + River migrations
make run-api            # terminal 1
make run-worker         # terminal 2
```

Shortcuts: `make setup` (env, Postgres, migrations, admin), `make dev`, `make build` (binaries in `bin/`),
and `make stack` (images plus Postgres, migrate, api and worker in containers; `make stack-down` stops it).
`make help` lists everything.

`EXAMPLES=strip` removes the `orders` and `acme` reference code. You can also run
`make strip-examples CONFIRM=yes` before any shared database has had migrations applied.

## Build your domain

```bash
make new-feature name=billing
```

Then follow the [Domain Blueprint](docs/DOMAIN_BLUEPRINT.md): event-storm, state machine, expand-only
migration, service with a fake repo, handler, jobs, wiring. It includes a complete worked example
(WhatsApp-based payments) with schema, flows, webhooks and a test plan.

## Capabilities

| Area | Included |
|---|---|
| HTTP | Typed operations, validation, RFC 9457 errors, request IDs, trusted-proxy real IP, per-IP and auth rate limits |
| Auth | argon2id passwords, PASETO v4.local tokens, refresh-token families, RBAC enforced in services |
| Data | pgx pool with tuning knobs, `sqlc` typed queries, goose migrations (verified up/down/up), keyset pagination |
| Async | River jobs, transactional outbox, named queues, idempotent workers, periodic cleanup |
| Integrations | Signed inbound webhooks with dedupe, outbound client with timeout, retry, breaker and limiter |
| Observability | slog JSON logs, OTLP traces, Prometheus metrics by route pattern, alert rules |
| Delivery | Distroless images, migration image, Kustomize (HPA, PDB, probes, network policy), CI with Trivy and SBOM |

## Configuration

Environment variables only, validated at startup with every error reported at once, secrets
redacted in logs. See [`.env.example`](.env.example). Scaling-related knobs:

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | required | Pooled traffic (can be PgBouncer) |
| `DIRECT_DATABASE_URL` | `DATABASE_URL` | Migrations, seed, worker (`LISTEN/NOTIFY`) |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | pgx default | API pool size per replica |
| `DB_MAX_CONN_LIFETIME` / `DB_MAX_CONN_IDLE_TIME` | `30m` / `5m` | Connection recycling for failover |
| `WORKER_CONCURRENCY` | `10` | Jobs per replica on the default queue |
| `WORKER_QUEUES` | empty | Named queues, e.g. `default:10,webhooks:5` |
| `WORKER_HEALTH_ADDR` | `:8081` | Worker probes (empty disables) |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `20` / `40` | Per-process limiter |

## Deploy

```bash
docker build -f deployments/Dockerfile -t ghcr.io/your-org/app:$V .
docker build -f deployments/Dockerfile --target migrate -t ghcr.io/your-org/app-migrate:$V .
kubectl kustomize deployments/k8s/overlays/production | kubectl apply -f -
```

Release order is migrate, then roll `api` and `worker`. Secrets come from a manager through
External Secrets or the CSI driver. Everything else, including backups, SLOs and runbooks, is
in [Operations](docs/OPERATIONS.md). Scaling levers in order are in [Scaling](docs/SCALING.md).

## Quality gates

Definition of done for every change:

```bash
make lint arch-check test test-integration migrate-verify openapi-check
sqlc diff && go mod tidy   # both must leave no diff
```

CI additionally runs `govulncheck`, builds and scans images, generates an SBOM, renders the
Kubernetes overlay, and runs a 4-way scaffold matrix (examples kept or stripped).

## Optional modules

Adopt only when its trigger is met: `make add-module NAME=<module>`.

| Module | Adopt when |
|---|---|
| `shared-ratelimit` | More than one API replica needs a global limit |
| `tenancy` | First multi-tenant customer |
| `ingest` | High-volume event ingestion |
| `notify-relay` | Cross-process `LISTEN/NOTIFY` fan-out |
| `contract` | Generated clients or consumer contract tests |

## Using this as a template

Every path has an owner in [`.template/OWNERSHIP`](.template/OWNERSHIP): `template`, `shared`, or
`project`. Edit `project` paths freely; record an ADR before touching `template` paths so upgrades
stay mergeable. Pull upstream changes with `make template-diff TAG=<version>` ([Upgrading](docs/UPGRADING.md)).
Contributors, human or agent, start with [AGENTS.md](AGENTS.md); behaviour changes need a
[CHANGELOG](CHANGELOG.md) entry and a `TEMPLATE_VERSION` bump.

## Documentation map

The `docs/` folder is a GitBook-compatible book ([`.gitbook.yaml`](.gitbook.yaml), [`SUMMARY.md`](docs/SUMMARY.md)).

| Read | For |
|---|---|
| [Architecture](docs/ARCHITECTURE.md) | Boundaries, request lifecycle, outbox |
| [Domain Blueprint](docs/DOMAIN_BLUEPRINT.md) | Adding business logic, worked payments example |
| [Operations](docs/OPERATIONS.md) | Secrets, backups, alerts, runbooks, readiness checklist |
| [Scaling](docs/SCALING.md) | Ordered scaling levers, queues, partitioning |
| [Porting](docs/PORTING.md) | Strangler-fig migration of legacy services |
| [Decisions](docs/adr/) | One short ADR per dependency or deviation |

## Verification status

Gates run locally for this revision: unit tests for `config`, `queue`, `app`, and a clean `go build`.
Run the full definition of done before tagging a release.
