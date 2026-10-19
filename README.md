# granite-go 

<p align="center">
  <em>Enterprise-grade Go service template powered by <code>net/http</code> (Go 1.22+ ServeMux), <code>pgx/v5</code> + <code>sqlc</code>, River Transactional Outbox, Huma v2 (OpenAPI 3.1), PASETO v4 + Argon2id, OpenTelemetry, Traefik v3 + Cloudflare Tunnel, and Kustomize.</em>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/CI-passing-emerald?style=flat-square" alt="CI Status" />
  <a href="https://go.dev" target="_blank">
    <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go" />
  </a>
  <a href="https://www.postgresql.org" target="_blank">
    <img src="https://img.shields.io/badge/PostgreSQL-17-336791?style=flat-square&logo=postgresql&logoColor=white" alt="PostgreSQL" />
  </a>
  <a href="https://riverqueue.com" target="_blank">
    <img src="https://img.shields.io/badge/River-Transactional_Outbox-2563EB?style=flat-square" alt="River Queue" />
  </a>
  <a href="https://huma.rocks" target="_blank">
    <img src="https://img.shields.io/badge/Huma_v2-OpenAPI_3.1-7C3AED?style=flat-square&logo=openapiinitiative&logoColor=white" alt="Huma OpenAPI 3.1" />
  </a>
  <a href="https://traefik.io" target="_blank">
    <img src="https://img.shields.io/badge/Traefik-v3_%2B_Cloudflare-24A1C1?style=flat-square&logo=traefikproxy&logoColor=white" alt="Traefik v3" />
  </a>
  <a href="https://opentelemetry.io" target="_blank">
    <img src="https://img.shields.io/badge/OTel-%2B_Prometheus-F5A800?style=flat-square&logo=opentelemetry&logoColor=black" alt="OpenTelemetry" />
  </a>
</p>

---

## Architecture Highlights

* **Strict Clean / Hexagonal Isolation:** Domain files (`<feature>.go`, `service.go`) import only the Go standard library and `google/uuid`. Inward dependency direction (`cmd -> app -> features/integrations -> platform`) is enforced in CI by `depguard` and `make arch-check`.
* **Atomic Transactional Outbox (No Dual Writes):** Business state changes and background jobs commit in **one** PostgreSQL transaction (`store.InTx` + River `InsertTx`), guaranteeing at-least-once delivery without message broker drift.
* **Contract-First Typed HTTP (`net/http` + Huma v2):** Typed request/response structs generate deterministic OpenAPI 3.1 specifications (`api/openapi.yaml`, verified via `make openapi-check`) and RFC 9457 `application/problem+json` error responses.
* **Hardened Authentication, RBAC & Multi-Tenant RLS:** Argon2id password hashing with constant-time `VerifyDummy` enumeration protection, PASETO `v4.local` access tokens, rotating SHA-256 refresh-token families with replay revocation, service-layer RBAC, and PgBouncer-safe PostgreSQL Row-Level Security (`store.InTenantTx`).
* **SQL-First Persistence (`pgx/v5` + `sqlc` + Goose):** Hand-written SQL compiled to type-safe Go code (zero ORM overhead), composite keyset pagination (`(created_at DESC, id DESC)`, never `OFFSET`), session timeout guards, and reversible UTC-timestamped migrations verified `up -> down -> up`.
* **Resilient Webhooks & Outbound Integrations:** HMAC-verified, deduplicated webhook ingestion (`inbound_events`) and an outbound HTTP client with context timeouts, jittered exponential backoff retries on idempotent calls, rate limiting, and a circuit breaker.
* **Leader-Elected Background Workers:** River worker pools with named queues (`WORKER_QUEUES`), sub-millisecond `LISTEN/NOTIFY` pickup, and leader-elected hourly retention sweeps (`maintenance.retention`).
* **Zero-Trust Edge & Kubernetes Delivery:** Built-in Docker/Podman Compose profiles for local development (`make stack`), Traefik v3 + Cloudflare Zero Trust Tunnel (`make stack-edge`), distroless runtime images, and production Kustomize manifests (HPA, PDB, NetworkPolicy).

```text
cmd/{api,worker,seed}          entrypoints only (run() pattern)
        │
   internal/app                composition roots, graceful shutdown, periodic retention
     │         │
 features   integrations       internal/<feature>   internal/integrations/<vendor>
     │         │
 internal/platform/*           httpx · store · queue · idempotency · ratelimit · auth · webhook · outbound · telemetry
```

---

## Quickstart & Project Scaffolding

### 1. Scaffold a New Project

You can initialize a new repository from `granite-go` using either Go's official `gonew` tool or `git clone`, followed by `make init`.

#### Option A: Via `gonew` (Recommended)

```bash
go install golang.org/x/tools/cmd/gonew@latest
gonew github.com/le-patrice/granite-go-template github.com/acme/billing-api
cd billing-api
make init
```

#### Option B: Via `git clone` (Interactive or One-Command CLI)

```bash
git clone https://github.com/le-patrice/granite-go-template billing-api
cd billing-api

# Interactive Copier-style prompt (asks for module path, app name, and examples=keep|strip):
make init

# Or non-interactive one-liner:
make init NAME=billing-api MODULE=github.com/acme/billing-api EXAMPLES=keep
```

* Set `EXAMPLES=strip` during `make init` (or run `make strip-examples CONFIRM=yes` later) to cleanly remove the `orders` and `acme` reference examples.
* `make init` records your project metadata in `.template/answers.json` and rewrites all module imports and placeholders automatically.

### 2. Configure & Boot the Local Stack

```bash
# One-click bootstrap: copies .env.example -> .env, starts Postgres, runs migrations, and seeds admin
make setup

# Run API and Worker locally in separate terminals:
make run-api      # Terminal 1 (API on :8080)
make run-worker   # Terminal 2 (Worker + probes on :8081)
```

Or run the entire stack in containers (Docker or Podman auto-detected):

```bash
# Local container stack (Postgres + migrations + API + Worker)
make stack

# Edge container stack (adds Traefik v3 + Cloudflare Tunnel; requires CLOUDFLARE_TUNNEL_TOKEN in .env)
make stack-edge
```

### 3. Scaffold Domain Features, Migrations & Optional Modules

```bash
# Generate a new domain feature (entity, service, repo, handler, unit test, UTC migration, keyset sqlc queries)
make new-feature name=billing

# Create a standalone UTC-timestamped SQL migration
make new-migration name=add_billing_index

# Enable optional enterprise modules when their trigger is met (e.g., PostgreSQL Row-Level Security)
make add-module NAME=tenancy TABLE=billing

# Regenerate typed SQL queries and OpenAPI 3.1 specification after edits
make sqlc
make openapi
```

Follow the **[Domain Blueprint](docs/DOMAIN_BLUEPRINT.md)** for the complete 10-part engineering workflow (event storming, state machines, transactional outbox flows, RBAC/RLS boundaries, and a worked WhatsApp payments implementation).

---

## Service Endpoints & Interfaces

| Service | Local Address | Access / Purpose |
| :--- | :--- | :--- |
| **REST API** | `http://localhost:8080/v1` | PASETO `v4.local` Bearer Token / Public Auth Routes |
| **Interactive OpenAPI Docs** | [http://localhost:8080/docs](http://localhost:8080/docs) | Enabled when `DOCS_ENABLED=true` (`local` default) |
| **OpenAPI 3.1 Spec** | [http://localhost:8080/openapi.yaml](http://localhost:8080/openapi.yaml) | Generated contract (`api/openapi.yaml`) |
| **API Health & Readiness** | [http://localhost:8080/healthz](http://localhost:8080/healthz) · [`/readyz`](http://localhost:8080/readyz) | Liveness and database readiness probes |
| **Prometheus Metrics** | [http://localhost:8080/metrics](http://localhost:8080/metrics) | Isolated registry (blocked on public Traefik edge) |
| **Worker Health & Readiness** | [http://localhost:8081/healthz](http://localhost:8081/healthz) · [`/readyz`](http://localhost:8081/readyz) | Background worker liveness & readiness probes |
| **Traefik Edge Proxy** | `http://localhost:8088` | Active with `make stack-edge` (`CLOUDFLARE_TUNNEL_TOKEN`) |

---

## Command Reference

| Command | Description |
| :--- | :--- |
| `make init` | Initialize project (`NAME=<app> MODULE=<mod> EXAMPLES=keep\|strip` or interactive prompt). |
| `make strip-examples` | Strip reference `orders` and `acme` example code (`CONFIRM=yes`). |
| `make setup` | One-click bootstrap: `.env`, Postgres container, migrations, and initial admin user. |
| `make dev` | Start Postgres, apply migrations, and run the API in the foreground. |
| `make stack` | Build images and launch `postgres`, `migrate`, `api`, and `worker` in containers. |
| `make stack-edge` | Launch full container stack plus `traefik` v3 and `cloudflared` Zero Trust tunnel. |
| `make stack-down` | Stop all stack and edge containers cleanly while preserving data volumes. |
| `make new-feature` | Scaffold a domain package, UTC migration, and keyset query (`name=<feature>`). |
| `make new-migration` | Create a UTC-timestamped goose SQL migration (`name=<migration>`). |
| `make add-module` | Adopt an opt-in module under `templates/modules/` (`NAME=<module>`). |
| `make sqlc` | Compile SQL queries in `db/queries/` into typed Go code in `internal/store/sqlc/`. |
| `make openapi` | Export deterministic OpenAPI 3.1 specification to `api/openapi.yaml`. |
| `make check` | Fast QA gate: `lint`, `arch-check`, `test` (`-race`), and `openapi-check`. |
| `make test-integration` | Run real PostgreSQL + River integration suite via testcontainers. |
| `make cover` | Run unit + integration coverage gate across `internal/` (`COVER_MIN=70`). |
| `make migrate-verify` | Verify `up -> down -> up` reversibility for all app and River migrations. |
| `make template-diff` | Inspect upstream template changes grouped by `.template/OWNERSHIP` (`TAG=<version>`). |

---

## Key Configuration Knobs

All configuration is parsed from environment variables at startup, validated with all errors joined at once, and redacted in structured logs. See [`.env.example`](.env.example).

| Variable | Default | Purpose |
| :--- | :--- | :--- |
| `DATABASE_URL` | *required* | Pooled application traffic (supports PgBouncer transaction mode). |
| `DIRECT_DATABASE_URL` | `DATABASE_URL` | Unpooled connection for migrations, `cmd/seed`, and River `LISTEN/NOTIFY`. |
| `DB_STATEMENT_TIMEOUT` / `DB_LOCK_TIMEOUT` / `DB_IDLE_IN_TX_TIMEOUT` | `30s` / `5s` / `30s` | Server-side Postgres session guards (`0` disables behind PgBouncer transaction mode). |
| `AUTH_TOKEN_KEY` | *required* | 64-char hex (32-byte) key for PASETO `v4.local` token encryption. |
| `TRUSTED_PROXIES` | empty | Comma-separated proxy CIDRs allowed to supply `CF-Connecting-IP` / `X-Forwarded-For`. |
| `WORKER_CONCURRENCY` / `WORKER_QUEUES` | `10` / empty | Per-replica worker concurrency or queue split (e.g. `default:10,webhooks:5`). |
| `RETENTION_IDEMPOTENCY_KEYS` / `RETENTION_INBOUND_EVENTS` / `RETENTION_REFRESH_TOKEN_GRACE` | `24h` / `720h` / `24h` | Retention windows swept hourly by the leader-elected `maintenance.retention` worker. |
| `PUBLIC_HOST` / `CLOUDFLARE_TUNNEL_TOKEN` | `localhost` / empty | Host rule and token used by `make stack-edge` (`traefik` + `cloudflared`). |

---

## Optional Enterprise Modules

Adopt an opt-in module only when its trigger is met: `make add-module NAME=<module>`.

| Module | Trigger to Adopt |
| :--- | :--- |
| [`tenancy`](templates/modules/tenancy/README.md) | Multiple customer organizations share tables and require PostgreSQL `FORCE ROW LEVEL SECURITY` (`store.InTenantTx`). |
| [`shared-ratelimit`](templates/modules/shared-ratelimit/README.md) | More than one API replica requires a shared global rate limit. |
| [`ingest`](templates/modules/ingest/README.md) | High-throughput event ingestion buffering. |
| [`notify-relay`](templates/modules/notify-relay/README.md) | Cross-process PostgreSQL `LISTEN/NOTIFY` fan-out. |
| [`contract`](templates/modules/contract/README.md) | Generated client SDKs or consumer contract test suites. |

---

## Quality Gates (Definition of Done)

Every pull request and release must pass:

```bash
make lint arch-check test test-integration migrate-verify openapi-check cover
sqlc diff && go mod tidy   # both must produce zero diff
```

---

## Documentation Index

The documentation suite under [`docs/`](docs/) is GitBook-compatible ([`.gitbook.yaml`](.gitbook.yaml), [`docs/SUMMARY.md`](docs/SUMMARY.md)):

* **[Introduction](docs/README.md)**: Overview of `granite-go` and reading order.
* **[Scaffolding & Lifecycle Guide](docs/SCAFFOLDING.md)**: Step-by-step project initialization (`gonew` / `make init`), stripping examples, feature generation, and `template-diff`.
* **[Domain Blueprint](docs/DOMAIN_BLUEPRINT.md)**: Authoritative 10-part standard for designing and wiring business domains, RBAC/RLS boundaries, and a worked WhatsApp payments architecture.
* **[Architecture Specification](docs/ARCHITECTURE.md)**: Package boundaries, dependency rules, request lifecycle, and transactional outbox mechanics.
* **[Operations Runbook](docs/OPERATIONS.md)**: Secret rotation, backups/PITR, SLO alerts, dead-letter replay, and production readiness checklist.
* **[Scaling Guide](docs/SCALING.md)**: Ordered scaling levers from single-node Postgres to PgBouncer, queue splitting, read replicas, and partitioning.
* **[Porting Legacy Services](docs/PORTING.md)**: Strangler-fig migration playbook and contract testing.
* **[Upgrading from the Template](docs/UPGRADING.md)**: Ownership tiers (`template`, `shared`, `project`) and upstream merge workflow.
* **[Architecture Decision Records](docs/adr/)**: Short ADRs (`0001`–`0008`) documenting every stack dependency and design decision.
