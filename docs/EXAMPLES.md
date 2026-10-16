# Reference Examples

This template ships with two removable reference examples that demonstrate how business features and vendor integrations are wired:

1. **`orders` (`internal/orders`):**
   - Owner-scoped authorization in `orders/service.go` (members see only their own orders; admins see all).
   - Transactional outbox (`store.InTx` + River `jobs.InsertTx`) enqueuing `order.created` in the same transaction as `INSERT INTO orders`.
   - Keyset pagination (`WHERE owner_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`).
   - Route-level request idempotency (`Idempotency-Key` header via `internal/platform/idempotency`).
2. **`acme` (`internal/integrations/acme`):**
   - Constant-time HMAC-SHA256 webhook signature verification (`X-Acme-Signature`) wired into `POST /v1/webhooks/{source}`.
   - Resilient outbound HTTP client built on `internal/platform/outbound`.

## Markers

Every line in shared files that wires an example is wrapped in paired markers:

- Go files: `// template:example:begin <name>` ... `// template:example:end <name>`
- Whole example files outside `internal/orders/` or `internal/integrations/acme/`: `// template:example:file <name>`
- YAML / `.env.example`: `# template:example:begin <name>` ... `# template:example:end <name>`
- SQL files: `-- template:example:begin <name>` ... `-- template:example:end <name>`

## Stripping Examples

You can strip examples either when initializing a project:

```bash
make init NAME=my-api MODULE=github.com/org/my-api EXAMPLES=strip
```

Or later (before applying migrations to any shared database):

```bash
make strip-examples CONFIRM=yes
```

Stripping removes `internal/orders/`, `internal/integrations/acme/`, `db/migrations/00005_create_orders.sql`, `db/queries/orders.sql`, `internal/app/idempotency_orders_integration_test.go`, and all marked wiring blocks, leaving core migrations `00001`–`00004` and the `users` feature intact.
