# Module: shared-ratelimit

## When to adopt

Adopt this module when you run multiple API replicas behind a load balancer and the in-process token bucket in `internal/platform/ratelimit` is no longer sufficient to enforce strict global per-IP or per-account quotas.

## What it adds

- Valkey/Redis-backed sliding-window or token-bucket rate limiter implementing the same middleware signature as `internal/platform/ratelimit`.

## How to remove

1. Switch `internal/app/api.go` back to `internal/platform/ratelimit.New`.
2. Remove the shared limiter package and `VALKEY_URL` config setting.
