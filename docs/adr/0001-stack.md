# ADR 0001: Locked Stack and Dependency Allowlist

## Status

Accepted.

## Context

`granite-go` is cloned to scaffold many backend services. Allowing arbitrary frameworks, ORMs, or logging libraries across projects makes upgrades, static analysis, and cross-team maintenance expensive.

## Decision

Lock the dependency stack to a strict allowlist:

- **HTTP:** Standard library `net/http` with Go 1.22+ `ServeMux` method/path patterns.
- **Database:** `github.com/jackc/pgx/v5` (`pgxpool`) with `sqlc`-generated queries (`internal/store/sqlc`) and `goose` SQL migrations (`db/migrations`).
- **Async jobs & transactional outbox:** `github.com/riverqueue/river` + `riverdriver/riverpgxv5` + `rivermigrate`.
- **Configuration:** `github.com/caarlos0/env/v11` with fail-fast startup validation.
- **Identifiers:** `github.com/google/uuid` (the only third-party package allowed in domain `<feature>.go` and `service.go` files).
- **Authentication & security:** `golang.org/x/crypto/argon2` (argon2id), `aidanwoods.dev/go-paseto` (PASETO v4.local), `golang.org/x/time/rate`.
- **Observability:** `log/slog` (JSON), OpenTelemetry (`go.opentelemetry.io/otel` + OTLP HTTP trace exporter), and `github.com/prometheus/client_golang` for `/metrics` via an isolated registry.
- **Testing:** `testing`, `github.com/testcontainers/testcontainers-go` (PostgreSQL module), and `go.uber.org/goleak`.

ORMs, third-party HTTP routers, `viper`, alternative loggers, `testify`, and DI containers are forbidden and blocked by `depguard` and `forbidigo`.
