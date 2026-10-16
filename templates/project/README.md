# {{APP_NAME}}

Go backend service scaffolded from `granite-go` (`{{MODULE_PATH}}`).

## Quick Start

```bash
cp .env.example .env
make up
make migrate
make run-api      # terminal 1
make run-worker   # terminal 2
```

## Common Commands

```bash
make help              # list all targets
make test              # unit tests (-race)
make test-integration  # integration tests (testcontainers)
make lint              # golangci-lint
make arch-check        # verify platform import boundaries
make migrate-verify    # up -> down -> up migration verification
make new-feature name=billing
```

## Documentation

- [AGENTS.md](AGENTS.md) — development rules and conventions
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — layering and dependency direction
- [docs/PORTING.md](docs/PORTING.md) — porting legacy domains
- [docs/UPGRADING.md](docs/UPGRADING.md) — pulling upstream template updates
