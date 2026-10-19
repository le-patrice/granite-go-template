# AGENTS.md: {{APP_NAME}}

Guidance for any coding agent or contributor working in this repo. Read this first. When it conflicts with a quick idea, this file wins. If it is wrong, change it in a PR with an ADR.

## What this is

A minimal, enterprise-ready Go service template: `net/http`, `pgx` + `sqlc`, River (Postgres-backed jobs and transactional outbox), PASETO + argon2id auth, OpenTelemetry. Domain logic is isolated from HTTP, SQL, queue, and vendor code.

## Commands

```bash
make help              # list targets
make up / make down    # local Postgres
make migrate           # app + River migrations
make migrate-verify    # up -> down -> up on a throwaway DB
make sqlc              # regenerate typed queries (commit the result)
make run-api / make run-worker
make seed-admin        # first admin from SEED_ADMIN_* env
make test              # unit tests, -race
make test-integration  # real Postgres via testcontainers
make cover             # unit + integration coverage floor (COVER_MIN=70)
make lint arch-check vuln
make openapi / make openapi-check # generate & verify openapi.yaml
make new-feature name=billing
make new-migration name=add_index # UTC timestamped goose migration
make init NAME=<app> MODULE=<module> EXAMPLES=keep|strip   # scaffold a project from the template (once)
make strip-examples CONFIRM=yes                              # remove example code
make template-diff TAG=<version>                             # upstream changes to template/shared paths
make add-module NAME=<module>                                # opt-in extras
```

Definition of done: `make lint arch-check test test-integration migrate-verify openapi-check` all pass, `sqlc diff` is clean, `go mod tidy` produces no diff.

## Layout and where code goes

| Path | Holds | May import |
|---|---|---|
| `cmd/*` | Entrypoints only (`run()` pattern) | `app`, `config` |
| `internal/app` | Composition roots, graceful shutdown | everything below |
| `internal/platform/*` | Reusable infrastructure, no business knowledge | stdlib, libraries, other `platform` |
| `internal/integrations/<vendor>` | Vendor adapters; vendor types live only here | `platform` |
| `internal/<feature>` | One business capability | `platform`, its own `sqlc` queries |
| `internal/store/sqlc` | Generated. **Never edit by hand.** | n/a |
| `db/migrations`, `db/queries` | SQL source of truth | n/a |
| `docs/adr` | One short record per dependency or deviation | n/a |

## Where domain logic lives

Inside the feature folder:

- `<feature>.go`: entities, rules, domain errors. Standard library and `google/uuid` only.
- `service.go`: use-cases and the small interfaces it needs, defined here (consumer side). No pgx, River, or `net/http`.
- `repo.go`: implements the service's interfaces with sqlc; translates driver errors to domain errors.
- `handler.go`: HTTP in and out; maps domain errors to status codes.
- `jobs.go`: River job args and workers (only if the feature has async work).

A service must be unit-testable with a fake repository, no database, no HTTP.

## Rules (enforced by CI where marked)

1. Dependencies point inward: `cmd -> app -> features/integrations -> platform`. Features never import each other or `integrations`. (`depguard`, `arch-check`)
2. `platform` never imports feature, integration, or `config` packages; it takes plain values. (`arch-check`)
3. Domain files import no DB driver, queue, or HTTP package. (`depguard`)
4. `context.Context` is the first parameter. No `context.Background()` on the request path. (`contextcheck`)
5. Wrap errors with `%w`; check with `errors.Is/As`; never return internal error text on 5xx.
6. Money is integer minor units. Time is UTC `timestamptz`.
7. Every migration has a working `Down`; extensions use `IF NOT EXISTS`. (`migrate-verify`)
8. A business change and the event it emits are written in **one transaction** (`pgx.BeginFunc` + River `InsertTx`). Jobs are at-least-once, so workers must be idempotent.
9. No bare `go func()` in handlers. Background work goes through River. Every goroutine has a stop path. (`goleak`)
10. Request traffic uses `DATABASE_URL` (pooled). Migrations and `LISTEN/NOTIFY` use `DIRECT_DATABASE_URL`.
11. All inbound bodies are size-capped and decoded with `httpx.Decode`. All webhooks verify signatures with `hmac.Equal` before any write.
12. No secrets, passwords, tokens, or hashes in logs or error messages.
13. No `init()`, no package-level mutable state, no `utils`/`common`/`pkg` packages.
14. Authorization decisions live in the service layer, not only in middleware.
15. Huma lives only in feature `handler.go` and `internal/platform/httpx`; feature packages never import `github.com/danielgtaylor/huma/v2` directly (`depguard`). Every endpoint is registered through `httpx.Register`. Run `make openapi` before committing; CI checks for drift with `make openapi-check`. Built-in docs UI is disabled outside `local`. Client errors use the unified RFC 9457 problem helper.

## Conventions

- Package names are short, lowercase, no stutter (`orders.Service`, not `orders.OrderService`).
- Accept interfaces, return concrete types. Keep interfaces small and define them where they are used.
- Handlers use typed Huma operations via `httpx.Register`: request/response structs, validation tags, and typed parameters.
- Log with `slog` and the context-aware methods (`InfoContext`). Include `request_id`.
- Errors on the wire are RFC 9457 problem+json from `httpx`.
- Lists use keyset pagination. Never `OFFSET` on large tables.
- Tests are table-driven. Integration tests carry `//go:build integration`.
- Run `make openapi` to regenerate `api/openapi.yaml`; `make openapi-check` verifies consistency.

## Adding a feature

1. `make new-feature name=<x>` and edit the generated files (creates a UTC timestamped migration and keyset query stub).
2. Write the migration (with `Down`) and queries in `db/`; run `make sqlc`. Standalone migrations use `make new-migration name=<y>`.
3. Put rules in `<x>.go`, use-cases in `service.go`, SQL access in `repo.go`, HTTP in `handler.go`.
4. Register the feature in `internal/app` (the only wiring point). Run `make openapi` to update `api/openapi.yaml`.
5. Unit-test the service with a fake repo; integration-test the repo and handler.
6. Run the definition-of-done commands.

## Adding an integration

Create `internal/integrations/<vendor>` with `payload.go`, `verify.go`, `client.go`. Use `platform/webhook` for inbound and `platform/outbound` for outbound calls. Wire it in `app`. Features depend on a small interface of their own, never on the vendor package.

## Template vs project

This repo is a **boilerplate** cloned to scaffold many projects. Two audiences, different rules.

**Template maintainers**
- Nothing project-specific outside the removable examples. Example wiring stays between `template:example` markers.
- Every path has an owner in `.template/OWNERSHIP` (`template`, `shared`, or `project`). Add new paths to it.
- A change to a `template` or `shared` path needs a CHANGELOG entry and a `TEMPLATE_VERSION` bump (semver). A change that requires downstream edits needs an "Upgrade notes" entry.
- `scaffold-smoke` must pass for both `EXAMPLES=keep` and `EXAMPLES=strip` before merge.
- Do not add a dependency, layer, or feature that most projects would delete. If it is optional, it is a module under `templates/modules/`.

**Project developers (after `make init`)**
- `project` paths are yours. Avoid editing `template` paths; if you must, record why in `docs/adr/` so upgrades stay mergeable. Edit `shared` paths freely and merge upstream changes by hand.
- Pull upstream changes with `make template-diff TAG=<version>`; see `docs/UPGRADING.md`.
- Run `make strip-examples CONFIRM=yes` only before any shared database has had migrations applied.

## Phases and migration

- Progress lives in `docs/PHASES.md`. Work phase by phase; each phase ends with a passing gate, a commit, and a `phase-N` tag. Do not skip past a failing gate.
- A phase edits only its own files plus the shared extension points (`app/`, `Makefile`, `.env.example`, `api/openapi.yaml`, `.golangci.yml`, `CHANGELOG.md`, `docs/PHASES.md`). Log any other edit to earlier work in the ledger.
- Porting a service: follow `docs/PORTING.md`. One domain per PR. Port first, improve later; never change behavior and structure in the same commit. Contract tests under `tests/contract/` define "same behavior".
- Schema changes are expand/contract: additive first, destructive only after cutover is stable.

## Dependencies

The allowlist is in `docs/adr/0001-stack.md`. Adding a dependency requires a one-paragraph ADR (what it removes, why stdlib is not enough). Forbidden: ORMs, web frameworks, viper, alternative loggers, DI frameworks.

## Security checklist for any change

- New endpoint: is auth required? Is the service enforcing ownership or role?
- New write endpoint: does it need `Idempotency-Key`?
- New input: size-capped, typed, unknown fields rejected?
- New outbound call: timeout from context, retries only if idempotent?
- New log line: could it contain a secret or personal data?

## Optional modules

See `templates/modules/*/README.md` (ingest, tenancy, shared rate limiter, notify relay, contract codegen). Adopt one with `make add-module NAME=<x>` only when its trigger in that README is met, and delete what you do not use.

## Never do

Edit generated code. Skip a failing check. Add an interface "just in case". Add a layer or shared package before two features need it. Change a rule above without an ADR.