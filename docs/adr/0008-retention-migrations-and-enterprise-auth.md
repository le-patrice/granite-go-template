# ADR 0008: Retention Sweeps, Timestamp Migrations, and Enterprise Auth Path

## Status

Accepted.

## Context

Running multiple enterprise projects from `granite-go` surfaced four lifecycle concerns:
1. Sequential migration numbering (`00006_...`) collides across upstream template releases and parallel feature branches.
2. `refresh_tokens` and `inbound_events` had no retention sweeps, and `idempotency_keys` cleanup ran as a per-replica goroutine rather than a leader-elected River job.
3. Enterprise deployments often require external OIDC/SSO identity providers (Entra ID, Okta, Keycloak, Auth0) with asymmetric JWKS verification rather than local password accounts and a symmetric PASETO key.
4. Documentation referenced `ENABLED_FEATURES` and `internal/app/legacy.go` without an implementation.

## Decision

1. **UTC timestamp migrations (`YYYYMMDDHHMMSS_<name>.sql`):** Preserve the initial sequential baseline (`00001`–`00005`), and require UTC timestamp versions for all subsequent template and project migrations via `make new-feature` and `make new-migration`. Goose orders versions numerically, so timestamps always sort after the baseline and never collide with upstream template updates.
2. **Leader-elected periodic retention (`maintenance.retention`):** Expose `PeriodicJobs` on `queue.WorkerConfig` and replace the per-replica idempotency goroutine with a single hourly River periodic job (`internal/app/retention.go`, `RunOnStart: true`) that sweeps `idempotency_keys` (`RETENTION_IDEMPOTENCY_KEYS`, default `24h`), `refresh_tokens` (`RETENTION_REFRESH_TOKEN_GRACE`, default `24h` after expiry so reuse detection remains effective), and `inbound_events` (`RETENTION_INBOUND_EVENTS`, default `720h`, indexed on `received_at`).
3. **Database session guards:** Configure `DB_STATEMENT_TIMEOUT` (`30s`), `DB_LOCK_TIMEOUT` (`5s`), and `DB_IDLE_IN_TX_TIMEOUT` (`30s`) on `store.PoolConfig` startup parameters (set to `0` behind PgBouncer in transaction mode unless added to `ignore_startup_parameters`).
4. **Enterprise authentication strategy:** Keep `internal/users` + PASETO `v4.local` as the zero-dependency default for standalone services. When a project requires enterprise SSO, adopt an OIDC verifier that validates JWTs against the provider's JWKS endpoint and populates `auth.Principal`, leaving `auth.RequireAuth` and service-layer authorization unchanged.
5. **Multi-tenant Row-Level Security (`store.InTenantTx`):** Provide `store.InTenantTx` and `store.InTenantTxOptions` (`internal/platform/store/tenancy.go`) which execute `SELECT set_config('app.tenant_id', $1, true)` inside `pgx.BeginFunc` (`is_local = true`, equivalent to `SET LOCAL`), paired with `templates/modules/tenancy/apply.sh` (`make add-module NAME=tenancy`). This gives multi-tenant domains PostgreSQL `FORCE ROW LEVEL SECURITY` defense-in-depth without leaking session state across PgBouncer transaction-pooled connections.
6. **Explicit route wiring:** Remove references to `ENABLED_FEATURES` and `internal/app/legacy.go`; strangler migrations route per domain at the ingress/reverse proxy or via explicit wiring in `internal/app`.

## Consequences

Tables with high write churn are bounded automatically across all worker replicas without duplicate sweeps. Upstream template migrations can be pulled via `make template-diff` without version collisions. Multi-tenant projects gain PgBouncer-safe Postgres RLS enforcement without adding overhead to single-tenant projects, and service authorization logic remains decoupled from whether tokens are verified locally via PASETO or externally via OIDC JWKS.
