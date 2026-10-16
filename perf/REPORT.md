# Performance Smoke Report

Record the results of running `make perf` against a local or staging environment.

## Environment

- **Date:** YYYY-MM-DD
- **Commit:** `<git-sha>`
- **Go version:** `<go version>`
- **Database:** PostgreSQL 17 (`deployments/compose.yaml`)
- **Rate limit config:** `RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`

## Scenario (`perf/smoke.js`)

| Metric | Threshold | Measured |
|---|---|---|
| `http_req_failed` | `< 1%` | |
| `http_req_duration` p50 | — | |
| `http_req_duration` p95 | `< 200ms` | |
| `http_req_duration` p99 | — | |
| `http_reqs` (RPS) | — | |

## Notes

- Note any rate-limiter adjustments required when running with higher VU counts.
