# Scaling

Scale in this order. Each step is cheap until the one before it is exhausted.

| Stage | Symptom | Lever |
|---|---|---|
| 1 | API CPU high | More API replicas (HPA is configured) |
| 2 | DB connections near limit | Lower `DB_MAX_CONNS`, add PgBouncer ([ADR 0004](adr/0004-pgbouncer-and-river.md)) |
| 3 | Job latency rising | `WORKER_CONCURRENCY`, worker replicas, then queue split |
| 4 | Slow list or report endpoints | Indexes, keyset pagination (already required), then read replica |
| 5 | Large append-only tables | Retention jobs, then time partitioning |
| 6 | Multiple customers | `tenancy` module |
| 7 | One database is the limit | Separate the queue database, then split by domain |

## API

The API is stateless. Replicas share nothing except Postgres. Settings that matter:

- `DB_MAX_CONNS`: the pool per replica. Budget: `replicas x DB_MAX_CONNS < max_connections - worker connections - headroom`.
- `GOMEMLIMIT` and `GOMAXPROCS` are set from container limits in the manifests, which avoids GC thrash and CPU throttling.
- `RATE_LIMIT_*` is per process. With N replicas the effective limit is N times higher. Adopt `templates/modules/shared-ratelimit` before relying on a global limit.

## PgBouncer

Put PgBouncer in transaction mode in front of Postgres for `DATABASE_URL`. Add
`default_query_exec_mode=cache_describe` to the URL. Keep `DIRECT_DATABASE_URL` pointed at
Postgres itself for migrations, the seed command, and the worker (River uses `LISTEN/NOTIFY`).

## Workers and queues

By default one `default` queue runs `WORKER_CONCURRENCY` jobs per replica. A slow third-party
API can fill every slot and delay unrelated jobs. Split queues:

```bash
WORKER_QUEUES=default:10,webhooks:5,payments:3
```

Concurrency is per replica per queue. The worker's pool is sized to the sum plus headroom.
Choose a queue per job in its args:

```go
func (SendRequestArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "payments", MaxAttempts: 8}
}
```

Rule of thumb: one queue per external dependency with its own rate limit, sized at or below
that vendor's allowed concurrency across all replicas.

**Queue-depth autoscaling.** Use KEDA with a PostgreSQL scaler against the direct database:

```yaml
triggers:
  - type: postgresql
    metadata:
      query: "SELECT COUNT(*) FROM river_job WHERE state = 'available'"
      targetQueryValue: "100"
```

## Database

- **Indexes first.** Every list query should have an index that matches its filter and sort. Check with `EXPLAIN (ANALYZE, BUFFERS)`.
- **Keyset pagination** only. Never `OFFSET` on growing tables.
- **Hot rows.** Serialise concurrent updates with `SELECT ... FOR UPDATE` inside `store.InTx`. Avoid global counters.
- **Retention.** Prune `idempotency_keys` (done hourly), old `inbound_events` payloads, and finished River jobs. River prunes its own finished jobs by default.
- **Partitioning.** When an append-only table passes tens of millions of rows, partition by `created_at` (monthly) and drop old partitions instead of deleting rows. Do this as an expand step: new partitioned table, dual write, backfill, switch.
- **Read replicas.** Route read-only list and report queries to a replica pool, never the webhook or payment write path. Accept replica lag in the UX.

## Capacity back-of-envelope

For a first estimate, one API replica (1 CPU) typically sustains a few thousand simple
requests per second, bounded by the database. Argon2id logins are deliberately expensive
(`ARGON2_MEMORY_KIB` x concurrent logins), so cap login concurrency with the auth rate limit
and size memory limits accordingly. Measure instead of trusting estimates: `perf/smoke.js`
is the starting k6 script.

## What not to add early

Redis, a service mesh, a message broker, or microservices. Postgres plus River covers queueing
and the outbox at this stage, and extra systems add failure modes. Add them when a measured
limit demands it, and record why in an ADR.
