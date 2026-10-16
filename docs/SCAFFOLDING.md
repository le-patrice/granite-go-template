# Scaffolding and Template Lifecycle Guide

This guide describes how to scaffold new projects from this template, manage examples, generate new features, and pull upstream template updates.

## 1. Quick Start

### Option A: `gonew` (Recommended)

`gonew` is Go's official project templating tool. It clones the repository and initializes `go.mod` with your target module path:

```bash
go install golang.org/x/tools/cmd/gonew@latest
gonew github.com/owner/granite-go github.com/myorg/my-service
cd my-service
make init
```

The scaffolding CLI detects the post-`gonew` environment automatically, infers the module name from `go.mod`, replaces remaining tokens across the repository, and optionally strips example code.

### Option B: Git Clone

```bash
git clone https://github.com/owner/granite-go my-service
cd my-service
make init NAME=my-service MODULE=github.com/myorg/my-service EXAMPLES=keep
```

`make init` creates `.template/answers.json` to record your choices and refuses to re-run unless `--force` is supplied.

---

## 2. Removing Example Code

The template includes two removable examples:
- `internal/orders`: domain entity with Postgres persistence, outbox events, and River workers.
- `internal/integrations/acme`: reference webhook integration with HMAC verification.

To strip all example code from a project:

```bash
make strip-examples CONFIRM=yes
```

This command:
1. Deletes example directories (`internal/orders`, `internal/integrations/acme`).
2. Deletes marked example files (`// template:example:file <name>`).
3. Strips inline marker blocks (`// template:example:begin <name>` ... `// template:example:end <name>`) across `internal/app/api.go`, `internal/app/worker.go`, `internal/config/config.go`, and `.env.example`.
4. Runs `gofmt -w .`, `make sqlc`, and `go mod tidy`.

---

## 3. Generating a New Feature

To generate a new business capability following the repo's layered architecture:

```bash
make new-feature name=billing
```

This creates:
- `internal/billing/billing.go`: domain entity, validation, and errors (zero external dependencies).
- `internal/billing/service.go`: use cases and repository interface.
- `internal/billing/repo.go`: PostgreSQL repository skeleton.
- `internal/billing/handler.go`: HTTP handler with Go 1.22 routing.
- `internal/billing/service_test.go`: unit test with fake repository.
- `db/migrations/<timestamp>_billing.sql`: initial goose migration.
- `db/queries/billing.sql`: sqlc query file.

Next steps after generation:
1. Define your table schema in the generated migration and run `make migrate`.
2. Write typed queries in `db/queries/billing.sql` and run `make sqlc`.
3. Wire the service and handler into `internal/app/api.go`.

---

## 4. Upstream Updates (`template-diff`)

The repository separates paths into three ownership tiers defined in `.template/OWNERSHIP`:

| Tier | Description | Downstream Rule |
|---|---|---|
| `[template]` | Template infrastructure, tooling, CI, dockerfile, platform packages | Adopt upstream improvements |
| `[shared]` | Extension points: `app/api.go`, `config/config.go`, `go.mod`, migrations | Review and merge manually |
| `[project]` | Domain packages (`internal/<feature>`), HTTP entrypoints | Owned by your project |

To inspect upstream changes compared with a given template version or tag:

```bash
make template-diff TAG=v0.2.0
```

The CLI outputs changed files classified into their respective ownership tiers so developers can adopt improvements without overwriting application code.
