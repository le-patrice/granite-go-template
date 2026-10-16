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
| 2.a | Config, telemetry, app/api.go, cmd/api | pending | | |
| 2.b | httpx | pending | | |
| 3.a | Core migrations, queries, sqlc | pending | | |
| 3.b | Store pool, tx, storetest | pending | | |
| 4.a | platform/auth | pending | | |
| 4.b | ratelimit, users, cmd/seed | pending | | |
| 5.a | platform/queue, orders example | pending | | |
| 5.b | Jobs, app/worker.go, cmd/worker | pending | | |
| 6 | Guards | pending | | |
| 7.a | platform/webhook | pending | | |
| 7.b | platform/outbound, integrations/acme | pending | | |
| 8.a | OpenAPI, deployments, perf, new-feature | pending | | |
| 8.b | init, strip-examples, template-diff, scaffold-smoke | pending | | |
| 9 | Guidance and migration kit | pending | | |
| 10 | Final verification | pending | | |

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
