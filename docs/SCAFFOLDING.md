# Scaffolding Guide — granite-go ({{APP_NAME}})

This guide describes how to scaffold new projects from `granite-go ({{APP_NAME}})`, manage examples, generate new features and migrations, and pull upstream template updates.

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
make init
```

### Interactive Questionnaire (Copier-style) vs. Non-Interactive CLI

When running `make init` in an interactive terminal, the scaffolding engine prompts with sensible defaults:

```text
Go module path (e.g. github.com/org/repo) [github.com/myorg/my-service]:
Application name [my-service]:
Include example code (orders, acme webhook)? [keep/strip] [keep]:
```

Pressing Enter accepts the bracketed default value. In automated environments (CI or scripts), pass variables directly to bypass the questionnaire:

```bash
make init NAME=my-service MODULE=github.com/myorg/my-service EXAMPLES=keep
```

`make init` creates `.template/answers.json` to record your choices and refuses to re-run unless `--force` is supplied.

---

## 2. Removing Example Code

The template includes two removable examples:
- `internal/orders`: domain entity with Postgres persistence, outbox events, and River workers.
- `internal/integrations/acme`: reference webhook integration with HMAC verification.

To strip all example code from a project (either during `make init EXAMPLES=strip` or afterward before shared database migrations are applied):

```bash
make strip-examples CONFIRM=yes
```

This command:
1. Deletes example directories (`internal/orders`, `internal/integrations/acme`).
2. Deletes marked example files (`// template:example:file <name>`).
3. Strips inline marker blocks (`// template:example:begin <name>` ... `// template:example:end <name>`) across `internal/app/api.go`, `internal/app/worker.go`, `internal/config/config.go`, `db/`, and `.env.example`.
4. Runs `gofmt -w .`, `make sqlc`, and `go mod tidy`.

---

## 3. Generating Features, Migrations, and Optional Modules

### New Domain Feature

To generate a new business capability following the repo's layered architecture:

```bash
make new-feature name=billing
```

This creates:
- `internal/billing/billing.go`: domain entity, validation, and errors (stdlib and `google/uuid` only).
- `internal/billing/service.go`: use cases and consumer-side repository interface.
- `internal/billing/repo.go`: PostgreSQL repository skeleton.
- `internal/billing/handler.go`: typed Huma HTTP handler skeleton.
- `internal/billing/service_test.go`: unit test with a fake repository.
- `db/migrations/<UTC_timestamp>_billing.sql`: reversible goose migration with composite `(created_at DESC, id DESC)` keyset index.
- `db/queries/billing.sql`: `sqlc` query file with `GetBilling` and keyset-paginated `ListBilling`.

### Standalone Migration

To create an empty UTC-timestamped goose migration with `-- +goose Up` and `-- +goose Down` sections:

```bash
make new-migration name=add_billing_status_idx
```

### Opt-In Enterprise Modules

When a project hits the trigger for an optional module under `templates/modules/` (for example, multi-tenant PostgreSQL Row-Level Security):

```bash
make add-module NAME=tenancy TABLE=billing
```

### Next Steps After Generation

1. Define your table schema in the generated migration and run `make migrate`.
2. Write typed queries in `db/queries/<feature>.sql` and run `make sqlc`.
3. Wire the service and handler into `internal/app/api.go` and run `make openapi`.

---

## 4. Upstream Updates (`template-diff`)

The repository separates paths into three ownership tiers defined in `.template/OWNERSHIP`:

| Tier | Description | Downstream Rule |
|---|---|---|
| `[template]` | Template infrastructure, tooling, CI, Dockerfile, `internal/platform/*` | Adopt upstream improvements |
| `[shared]` | Extension points: `internal/app/*`, `internal/config/*`, `go.mod`, migrations | Review and merge manually |
| `[project]` | Domain packages (`internal/<feature>`), integrations, HTTP entrypoints | Owned by your project |

To inspect upstream changes compared with a given template version or tag:

```bash
make template-diff TAG=v0.4.1
```

The CLI outputs changed files classified into their respective ownership tiers so teams can adopt platform upgrades without overwriting application domain code.
