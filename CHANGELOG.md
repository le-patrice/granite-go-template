# Changelog

All notable changes to this template are recorded here. The template uses
semantic versioning, recorded in `TEMPLATE_VERSION`. Any change that needs
edits in downstream projects carries an "Upgrade notes" entry.

## [Unreleased]

## [0.4.1]

### Added

- `DB_STATEMENT_TIMEOUT`, `DB_LOCK_TIMEOUT`, `DB_IDLE_IN_TX_TIMEOUT` session guards (`store.PoolConfig`).
- `queue.WorkerConfig.PeriodicJobs` passes River periodic jobs to the worker client (leader-elected across replicas).
- Hourly periodic retention sweep (`maintenance.retention` in `internal/app/retention.go`) pruning `idempotency_keys` (`RETENTION_IDEMPOTENCY_KEYS`), `refresh_tokens` (`RETENTION_REFRESH_TOKEN_GRACE`), and `inbound_events` (`RETENTION_INBOUND_EVENTS`), plus `20261019000000_inbound_events_received_at_idx.sql`.
- `make new-migration name=<x>` for UTC timestamped goose SQL migrations, and composite keyset index/query generation in `make new-feature`.
- `store.InTenantTx` and `store.InTenantTxOptions` (`internal/platform/store/tenancy.go`) for PgBouncer-safe PostgreSQL Row-Level Security (`SET LOCAL app.tenant_id`), plus `templates/modules/tenancy/apply.sh` (`make add-module NAME=tenancy`).
- ADR 0008 covering retention sweeps, UTC timestamp migrations, session guards, multi-tenant RLS, and the enterprise OIDC/JWKS authentication path.

### Changed

- `make cover` runs unit and integration tests together and fails below `COVER_MIN` (default 70%); CI integration job uses it.
- CI tools (`golangci-lint`, `govulncheck`, `sqlc`, `goose`) are pinned instead of `@latest`.
- Dockerfile pins `golang:1.26-alpine`, `alpine:3.22` and uses `GOTOOLCHAIN=local`.
- `docs/DOMAIN_BLUEPRINT.md`: added section 5.1 detailing RBAC and multi-tenant RLS responsibilities across HTTP, domain, service, and repository layers; section 6 is now a status table (done versus open gaps found in review) and section 10 covers running several projects from the template. Corrected the list cursor (keyset on `created_at, id`) and noted that `platform/webhook` has no GET handshake.
- Removed unimplemented `ENABLED_FEATURES` and `internal/app/legacy.go` references from `AGENTS.md` and `docs/PORTING.md`.

### Fixed

- `httpx.AdaptMiddleware` now passes the context built by earlier middleware to later ones. Previously the principal set by `RequireAuth` was lost before the idempotency middleware, so `POST /v1/orders` with `Idempotency-Key` returned 401.
- Integration tests now expect 422 for Huma schema and parameter validation failures (unknown body fields, malformed path and query values), matching the current wire behavior.
- `make vuln` runs a pinned `govulncheck` through `go run`, so a stale binary on `PATH` cannot break it.

### Upgrade notes

- Behind PgBouncer in transaction mode, set `DB_STATEMENT_TIMEOUT=0`, `DB_LOCK_TIMEOUT=0`, and `DB_IDLE_IN_TX_TIMEOUT=0` (or allow them via `ignore_startup_parameters`), otherwise connections are rejected.
- Apply the new migration (`20261019000000_inbound_events_received_at_idx.sql`) before running the updated worker retention job.

## [0.4.0]

### Added

- `docs/DOMAIN_BLUEPRINT.md`: method, checklists and a worked WhatsApp payments example for adding business domains.
- `docs/OPERATIONS.md`, `docs/SCALING.md`, ADR 0007, and a GitBook structure (`.gitbook.yaml`, `docs/SUMMARY.md`, `docs/README.md`).
- Worker health listener (`WORKER_HEALTH_ADDR`, default `:8081`) with `/healthz` and `/readyz`.
- Named River queues via `WORKER_QUEUES`; `queue.WorkerConfig.Queues` and `TotalWorkers`.
- Pool tuning: `DB_MAX_CONNS`, `DB_MIN_CONNS`, `DB_MAX_CONN_LIFETIME`, `DB_MAX_CONN_IDLE_TIME`.
- Kustomize manifests (`deployments/k8s`), Prometheus alert rules, and a `migrate` Docker target.
- Makefile: styled `help`, `setup`, `dev`, `check`, `build`, `build-images`, `stack`, `ps`, `health`, `db-shell`, `db-backup` and related targets; compose `app` profile (migrate, river-migrate, api, worker).
- CI: image build, Trivy scan, SBOM, kustomize render; Dependabot configuration.

### Changed

- `store.Pinger` accepts the small `store.Pinging` interface instead of `*pgxpool.Pool`.
- README rewritten for advanced users with a documentation map.

### Upgrade notes

- New variables are optional with safe defaults; copy the new entries from `.env.example`.
- The worker now listens on `:8081` by default. Set `WORKER_HEALTH_ADDR=` to disable, or change the port if it conflicts.
- Replace `{{APP_NAME}}` image names in `deployments/k8s/base/kustomization.yaml` or delete `deployments/k8s` if you do not use Kubernetes.

## [0.3.0]

### Added

- Huma v2 integration in `internal/platform/httpx`: Typed operation registration via `httpx.Register` and `httpx.NewAPI`.
- Code-first OpenAPI 3.1 generation via `cmd/openapi`, `make openapi`, and `make openapi-check`.
- Unified RFC 9457 Problem error model across all client errors with field-level details.
- Suppression of `$schema` injection on response payloads.
- Automated route coverage test in `internal/app/routes_test.go`.
- `huma-boundary` depguard rule enforcing that feature packages never import `github.com/danielgtaylor/huma/v2` directly.
- Micro-benchmark in `internal/platform/httpx/benchmark_test.go` measuring Huma vs bare `net/http`.
- ADR 0006 and `templates/modules/huma/README.md`.

### Upgrade notes

- Downstream projects must port `handler.go` files to use `httpx.Register` and typed input/output structs. Domain logic (`<feature>.go`, `service.go`, `repo.go`) requires zero changes.

## [0.2.0]

### Added

- `cmd/scaffold`: Pure-Go CLI replacing shell scripts for `init`, `strip-examples`, `new-feature`, and `template-diff`.
- `gonew` support: Automatic detection and parameter inheritance when initialized via `gonew`.
- Answers tracking: `.template/answers.json` records initial scaffolding configuration and prevents accidental re-runs.
- Path ownership definitions in `.template/OWNERSHIP` and origin tracking in `.template/origin`.
- 4-way `scaffold-smoke` test matrix (`scripts/scaffold-smoke.sh`) and CI workflow integration.
- `docs/SCAFFOLDING.md` guide and ADR 0005.

### Upgrade notes

- Downstream projects can adopt `cmd/scaffold` by copying `cmd/scaffold/`, `.template/`, and updating `Makefile` targets.

### Added

- Repository skeleton: Go module, Makefile, golangci-lint v2 configuration
  with architecture rules, CI workflow, ignore files, and the architecture
  check script.

### Upgrade notes

None. This is the initial release.
