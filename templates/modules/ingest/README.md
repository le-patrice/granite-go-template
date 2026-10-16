# Module: ingest

## When to adopt

Adopt this module when your service needs a dedicated high-throughput bulk-write binary (`cmd/ingest`) that batches rows via PostgreSQL `COPY FROM` (`pgx.CopyFrom`) separately from the request/response API.

## What it adds

- `cmd/ingest/main.go`: standalone entrypoint with graceful drain.
- `internal/app/ingest.go`: composition root wiring a bounded batch buffer and `pgxpool` connection pool.

## How to remove

1. Delete `cmd/ingest/` and `internal/app/ingest.go`.
2. Remove the `ingest` build line from `deployments/Dockerfile` if added.
