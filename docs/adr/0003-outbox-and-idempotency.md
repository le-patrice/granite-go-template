# ADR 0003: Transactional Outbox, Webhooks, and Idempotency

## Status

Accepted.

## Context

Write endpoints and webhook receivers must not lose background jobs when a process crashes after committing a database row, and client retries must not create duplicate side effects.

## Decision

1. **Transactional outbox via River:** Feature repositories open a `pgx.Tx` with `store.InTx` (`pgx.BeginFunc`) and call both sqlc queries and `jobs.InsertTx` inside the same transaction. River workers process jobs at-least-once and cancel jobs (`river.JobCancel`) when the target entity no longer exists.
2. **Inbound webhook ingestion:** `platform/webhook.Ingest` verifies the sender's HMAC signature in constant time (`hmac.Equal`) before touching the database, then in a single transaction inserts into `inbound_events` (`ON CONFLICT (source, external_id) DO NOTHING RETURNING id`) and enqueues a `webhook.event` job. Duplicate deliveries return `200 OK` without enqueueing; database failures return `503 Service Unavailable` so the sender retries.
3. **Request idempotency:** Opt-in `POST` routes require `Idempotency-Key`. The middleware hashes `method + "\n" + request_uri + "\n" + body` with SHA-256, reserves `(scope, key)` atomically in `idempotency_keys`, returns `409 Conflict` (`Retry-After: 1`) while in flight, returns `422 Unprocessable Entity` if the same key is reused with a different request hash, releases the key on `>= 500` or panic, and replays stored responses with `Idempotent-Replayed: true`. The sqlc store implementation lives in `internal/app` so `internal/platform/idempotency` remains free of `internal/store/sqlc` imports.
