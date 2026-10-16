# Operations

Production runbook for services scaffolded from this template. Manifests live in
`deployments/k8s`, alert rules in `deployments/observability/alerts.yaml`.

## Deployment topology

| Unit | Command | Scales on | Probes |
|---|---|---|---|
| `api` | `/bin/api` | CPU (HPA), request rate | `/healthz`, `/readyz` on `:8080` |
| `worker` | `/bin/worker` | CPU, or queue depth (see [Scaling](SCALING.md)) | `/healthz`, `/readyz` on `WORKER_HEALTH_ADDR` (`:8081`) |
| `migrate` | goose then `/bin/worker migrate` | n/a, runs once per release | Job completion |

Release order: **migrate, then roll api and worker**. Migrations are expand/contract, so the
previous version keeps running while the new schema is applied. Destructive changes ship in a
later release, after the old version is gone.

Build the images:

```bash
docker build -f deployments/Dockerfile -t ghcr.io/your-org/app:$VERSION .
docker build -f deployments/Dockerfile --target migrate -t ghcr.io/your-org/app-migrate:$VERSION .
```

Render and apply:

```bash
kubectl kustomize deployments/k8s/overlays/production | kubectl apply -f -
```

## Secrets

The app reads secrets from environment variables only. Do not commit them, and do not put
them in the ConfigMap. Populate the `app-secrets` Secret from a manager:

| Source | Mechanism |
|---|---|
| AWS Secrets Manager / GCP Secret Manager / Azure Key Vault / Vault | External Secrets Operator (`ExternalSecret` to `app-secrets`) |
| Any of the above, mounted | Secrets Store CSI driver with `syncSecret` |
| GitOps without a manager | Sealed Secrets or SOPS |

Required keys: `DATABASE_URL`, `DIRECT_DATABASE_URL`, `AUTH_TOKEN_KEY`, plus every
`WEBHOOK_*_SECRET` and vendor token.

**Rotation**
- `AUTH_TOKEN_KEY`: a rotation invalidates all PASETO access tokens. Rotate in a low-traffic window; clients refresh with their refresh token.
- Webhook secrets: add the new secret at the provider first when it supports two active secrets, then deploy, then retire the old one.
- Database credentials: create the new role, update the Secret, roll the pods, drop the old role.

## Database

- Use managed Postgres 14+ with **point-in-time recovery** enabled.
- Retention: at least 7 days of PITR and daily snapshots kept 30 days.
- **Restore drill each quarter**: restore into a scratch instance, run `make migrate-verify` against it, and check row counts of `users` and your core tables. Record the time taken. That is your real RTO.
- Targets to set per project: RPO (PITR gives about 5 minutes), RTO (measured by the drill).
- Connection budget: `replicas x DB_MAX_CONNS` for the API plus `replicas x (queue concurrency + 4)` for workers must stay below `max_connections` (or PgBouncer's pool). The `PostgresConnectionsHigh` alert fires at 80%.
- Autovacuum: `river_job`, `idempotency_keys`, and `inbound_events` churn. Lower `autovacuum_vacuum_scale_factor` (for example `0.05`) on them if bloat appears.

## Observability

- Metrics: `GET /metrics` on the API. Scrape annotations are on the pod.
- Traces: set `OTEL_EXPORTER_OTLP_ENDPOINT` to an OTLP/HTTP collector.
- Logs: JSON on stdout, carrying `request_id`.
- Alerts: `deployments/observability/alerts.yaml`.

### Queue metrics (postgres_exporter)

The worker does not export queue metrics itself. Add this custom query to `postgres_exporter`:

```yaml
river_jobs:
  query: |
    SELECT queue,
           COALESCE(EXTRACT(EPOCH FROM now() - MIN(scheduled_at)) FILTER (WHERE state = 'available'), 0) AS available_oldest_age_seconds,
           COUNT(*) FILTER (WHERE state = 'retryable') AS retryable,
           COUNT(*) FILTER (WHERE state = 'available') AS available
    FROM river_job GROUP BY queue
  metrics:
    - queue: {usage: LABEL, description: "Queue name"}
    - available_oldest_age_seconds: {usage: GAUGE, description: "Age of oldest runnable job"}
    - retryable: {usage: GAUGE, description: "Jobs waiting for retry"}
    - available: {usage: GAUGE, description: "Runnable jobs"}
```

This exposes `river_jobs_available_oldest_age_seconds`, `river_jobs_retryable`, and
`river_jobs_available`. The `JobsDiscarded` alert needs a discarded-count series; add a
`COUNT(*) FILTER (WHERE state = 'discarded')` column named `discarded_total` the same way.

### Suggested SLOs

| SLI | Target |
|---|---|
| Availability (non-5xx / all) | 99.9% over 30 days |
| Latency (p95 under 500 ms on core routes) | 99% of windows |
| Job freshness (oldest runnable job under 60 s) | 99% of windows |

## Supply chain

CI builds both images, runs Trivy (fails on fixable HIGH/CRITICAL), and uploads an SBOM.
`govulncheck` runs on the source. Dependabot raises weekly updates for Go modules, Actions,
and the Dockerfile. To sign images, add `cosign sign` to your release workflow; it is
project-specific because it needs your registry identity.

## Runbooks

### 5xx spike
1. Check `/readyz` and the `PostgresConnectionsHigh` alert. A failing readiness check means the database is unreachable or saturated.
2. Look at recent deploys; roll back with `kubectl rollout undo deployment/api`. Because migrations are additive, rollback is safe.
3. Search logs by `request_id` for the failing route.

### Queue backlog growing
1. Is the vendor failing? Check retry counts and the `outbound` breaker logs.
2. Is it slow, not failing? Raise `WORKER_CONCURRENCY` or add replicas, then watch the connection budget.
3. One slow integration starving others? Split it into its own queue with `WORKER_QUEUES` (see [Scaling](SCALING.md)).

### Replay a discarded job
```sql
-- Inspect
SELECT id, kind, attempt, errors[array_length(errors,1)] FROM river_job WHERE state = 'discarded' ORDER BY id DESC LIMIT 20;
-- Replay after the fix is deployed (jobs are at-least-once, workers are idempotent)
UPDATE river_job SET state = 'available', attempt = 0, scheduled_at = now(), finalized_at = NULL WHERE id = ANY($1);
```
Prefer River's UI or `river` CLI for bulk operations.

### Replay an inbound webhook
Webhook payloads are stored in `inbound_events`. Enqueue a new `webhook.event` job for the row
ID, or re-send from the provider. Dedupe on `(source, external_id)` prevents double inserts.

### Rotate a leaked secret
Rotate at the source, update `app-secrets`, roll `api` and `worker`, then review logs for use of
the old credential.

### Database failover
Pools recycle connections every `DB_MAX_CONN_LIFETIME` (30 minutes by default), and readiness
fails fast while the database is away, so traffic drains from unhealthy pods. No manual action
is normally needed; confirm `/readyz` recovers.

## Production readiness checklist

- [ ] `ENV=production`, `DOCS_ENABLED=false`, `TRUSTED_PROXIES` set to the ingress CIDR
- [ ] Secrets from a manager, rotation procedure tested
- [ ] PITR on, restore drill done and recorded
- [ ] `DB_MAX_CONNS` sized against `max_connections` or PgBouncer
- [ ] Alerts loaded and routed to a pager
- [ ] OTLP collector configured
- [ ] Ingress terminates TLS and enforces a body size limit at the edge too
- [ ] `shared-ratelimit` module adopted if running more than one API replica
- [ ] Reconciliation job in place for any money flow
