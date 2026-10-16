# Module: notify-relay

## When to adopt

Adopt this module when you need low-latency Postgres `LISTEN/NOTIFY` fan-out to connected clients (such as WebSockets or SSE streams) using a dedicated direct database connection (`DIRECT_DATABASE_URL`).

## What it adds

- `internal/platform/store/listener.go`: managed `pgx` connection loop listening on a channel with automatic reconnect and backoff.

## How to remove

1. Stop registering the listener in `internal/app`.
2. Delete `internal/platform/store/listener.go`.
