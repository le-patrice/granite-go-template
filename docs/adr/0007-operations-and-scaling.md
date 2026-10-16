# ADR 0007: Operations and Scaling Defaults

## Status

Accepted.

## Context

The template had no deployment manifests beyond compose, no worker health endpoint, a single
River queue, and no alerting or supply-chain checks. Enterprise projects need these on day one,
but most should not take on new runtime dependencies for them.

## Decision

1. **No new Go dependencies.** Everything below uses the standard library and existing packages.
2. **Worker health listener** (`WORKER_HEALTH_ADDR`, default `:8081`) serves `/healthz` and `/readyz` using `httpx.Healthz` and `httpx.Readyz`. Empty disables it. `store.Pinger` now accepts a small `Pinging` interface.
3. **Named queues** via `WORKER_QUEUES` (`name:concurrency,...`), so one slow vendor cannot starve other jobs. Unset keeps the single `default` queue.
4. **Pool tuning** via `DB_MAX_CONNS`, `DB_MIN_CONNS`, `DB_MAX_CONN_LIFETIME`, `DB_MAX_CONN_IDLE_TIME`. Zero keeps pgx defaults for sizes.
5. **Kustomize manifests** under `deployments/k8s` (base plus a production overlay): HPA, PDB, probes, read-only root filesystem, default-deny network policy, and a migration Job. Kustomize was chosen over Helm because it needs no templating language and renders with `kubectl`.
6. **Migration image** (`--target migrate`) holds goose and `db/migrations`; River migrations run from the app image.
7. **Alert rules** in `deployments/observability/alerts.yaml`. Queue metrics come from a documented `postgres_exporter` query rather than a Prometheus dependency inside the worker.
8. **CI**: image build, Trivy scan, SBOM, kustomize render, and Dependabot.

## Consequences

Projects get a production path without choosing a platform first. Teams not on Kubernetes can
delete `deployments/k8s`. Queue and pool settings are environment-only, so no code change is
needed to tune them. The shared rate limiter, tenancy, and audit log remain opt-in modules.
