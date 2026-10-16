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
| 1 | Skeleton | pending | | |
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

1. Template repo uses placeholder tokens `{{APP_NAME}}` and `{{MODULE_PATH}}` in documentation; Go files use a valid stand-in module path (see Phase 1) that `scripts/init.sh` rewrites.
2. Chat protocol: one phase per reply, gate output pasted back before "continue".
