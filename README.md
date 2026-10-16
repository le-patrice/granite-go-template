# granite-go ({{APP_NAME}})

Production-grade Go service boilerplate (`{{MODULE_PATH}}`) built on `net/http` (Go 1.22+ `ServeMux`), `pgx/v5` + `sqlc`, River (transactional job queue and outbox), argon2id + PASETO v4 authentication, OpenTelemetry, and Prometheus.

## Scaffolding a New Project

Clone this template repository (or use `gonew`) and initialize in place:

```bash
make init NAME=billing-api MODULE=github.com/acme/billing-api EXAMPLES=keep
```

Use `EXAMPLES=strip` to remove the `orders` and `integrations/acme` reference examples during initialization, or run `make strip-examples CONFIRM=yes` later before applying migrations to a shared database.

## Local Development

```bash
cp .env.example .env
make up
make migrate
make run-api      # terminal 1
make run-worker   # terminal 2
```

## Verification

```bash
make lint
make arch-check
make test
make test-integration
make migrate-verify
make vuln
```

## Documentation

- [AGENTS.md](AGENTS.md) — rules, conventions, and contributor guidance
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — package boundaries and dependency flow
- [docs/EXAMPLES.md](docs/EXAMPLES.md) — reference examples and how to strip them
- [docs/PORTING.md](docs/PORTING.md) — strangler-fig porting playbook for legacy services
- [docs/UPGRADING.md](docs/UPGRADING.md) — pulling upstream template upgrades
