# Module: tenancy

## When to adopt

Adopt this module when multiple customer organizations share tables and you require defense-in-depth isolation through PostgreSQL Row-Level Security (RLS) in addition to service-layer scoping.

## What is included

- `internal/platform/store/tenancy.go` (built-in): `store.InTenantTx` and `store.InTenantTxOptions` bind `app.tenant_id` locally to the current transaction via `SELECT set_config('app.tenant_id', $1, true)` (`SET LOCAL`), which automatically clears on `COMMIT` or `ROLLBACK` and is safe with PgBouncer transaction pooling.
- `templates/modules/tenancy/apply.sh`: generates a UTC-timestamped goose migration enabling `ROW LEVEL SECURITY`, `FORCE ROW LEVEL SECURITY`, and the `tenant_id` policy on a target table:
  ```bash
  make add-module NAME=tenancy TABLE=invoices
  ```

## Runtime requirement

PostgreSQL superusers and roles with `BYPASSRLS` bypass RLS even when `FORCE ROW LEVEL SECURITY` is enabled. Ensure the application's `DATABASE_URL` connects as a role created with `NOSUPERUSER NOBYPASSRLS`.

## How to remove

1. Drop the RLS policies in a new migration (`DROP POLICY ...; ALTER TABLE ... NO FORCE ROW LEVEL SECURITY; ALTER TABLE ... DISABLE ROW LEVEL SECURITY;`).
2. Switch repository callers from `store.InTenantTx` back to `store.InTx`.
