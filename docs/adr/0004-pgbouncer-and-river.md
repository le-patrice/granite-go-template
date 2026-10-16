# ADR 0004: PgBouncer Transaction Mode and River Direct Connections

## Status

Accepted.

## Context

Production deployments typically place PgBouncer in transaction-pooling mode in front of PostgreSQL for HTTP request traffic, while migrations and River's `LISTEN/NOTIFY` job notifier require session-level features.

## Decision

1. **Dual database URLs:** `DATABASE_URL` is used by `cmd/api` for pooled request traffic. `DIRECT_DATABASE_URL` (falling back to `DATABASE_URL` in local development) is used by `make migrate`, `cmd/seed`, and `cmd/worker`.
2. **PgBouncer prepared statements:** When `DATABASE_URL` points to PgBouncer in transaction mode without protocol-level prepared statement support, set `default_query_exec_mode=cache_describe` (or `exec`) in the `DATABASE_URL` query string. `pgxpool.ParseConfig` parses this setting directly from the connection string.
3. **River notifier:** `cmd/worker` connects via `DIRECT_DATABASE_URL` so River's `LISTEN/NOTIFY` leadership and notification listener operate over direct PostgreSQL connections. In environments where only a transaction pooler is reachable, River automatically falls back to polling (`FetchPollInterval`).
