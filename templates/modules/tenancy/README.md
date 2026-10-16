# Module: tenancy

## When to adopt

Adopt this module when multiple customer organizations share tables and you require defense-in-depth isolation through PostgreSQL Row-Level Security (RLS) in addition to service-layer scoping.

## What it adds

- `internal/platform/store/tenancy.go`: transaction helper that sets `SET LOCAL app.tenant_id = ...` before running queries.
- Migration helpers for enabling `ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY` on tenant-scoped tables.

## How to remove

1. Drop the RLS policies in a new migration (`ALTER TABLE ... DISABLE ROW LEVEL SECURITY`).
2. Delete `internal/platform/store/tenancy.go` and switch callers back to `store.InTx`.
