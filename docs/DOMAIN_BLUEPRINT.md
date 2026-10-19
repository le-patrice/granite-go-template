# Domain Blueprint

How to add business logic to this template, end to end, and a worked example:
**WhatsApp-based payments** (customer pays through a WhatsApp conversation; the
service reconciles the result from a payment provider).

Read `AGENTS.md` and `docs/ARCHITECTURE.md` first. Where this file conflicts with
`AGENTS.md`, `AGENTS.md` wins.

---

## 1. The Method (applies to every domain)

Work in this order. Each step has a gate.

| # | Step | Output | Gate |
|---|------|--------|------|
| 1 | **Event-storm** the domain: commands, events, actors, invariants, external systems | A one-page table (section 2) | Every state change has an owner |
| 2 | **Draw the boundary**: which capabilities are *features* (own data + rules) and which are *integrations* (vendor adapters) | Package list | Features never import each other or vendors |
| 3 | **Model the state machine** for each aggregate | Allowed transitions in `<feature>.go` | Illegal transitions are unrepresentable or return a domain error |
| 4 | **Schema** (expand-only): tables, constraints, indexes, `Down` | `db/migrations/YYYYMMDDHHMMSS_*.sql` (`make new-feature` / `make new-migration`) | `make migrate-verify` |
| 5 | **Queries** | `db/queries/*.sql`, `make sqlc` | `sqlc diff` clean |
| 6 | **Service with a fake repo** | `service.go` + table-driven tests | Tests pass with no DB or HTTP |
| 7 | **Repo, handler, jobs** | `repo.go`, `handler.go`, `jobs.go` | `make test-integration` |
| 8 | **Wire in `internal/app`**, add env vars | `api.go`, `worker.go`, `.env.example` | `make openapi` |
| 9 | **Operate**: metrics, alerts, runbook | Section 9 checklist | Definition of done in `AGENTS.md` |

### Feature vs integration vs platform decision rule

- Has its own tables and business rules, so it is a **feature** (`internal/payments`).
- Talks to a vendor's wire format, so it is an **integration** (`internal/integrations/whatsapp`).
- Reusable with zero business knowledge, so it is **platform**. Do not create new platform packages until two features need it.
- Features need vendor behaviour through a **small interface the feature defines** (consumer side), implemented by an adapter wired in `app`.

---

## 2. Domain Discovery Template

Copy this table per domain before writing code.

| Command (actor) | Preconditions / invariants | Event emitted | Async? | External call |
|---|---|---|---|---|
| ... | ... | ... | ... | ... |

Also answer:

- **Money**: currencies, rounding, fees, refunds, partial captures? (integer minor units; never float)
- **Identity**: who owns the aggregate; is the end user an authenticated `users` principal or an external identity (phone number)?
- **Consistency**: what must be atomic with the event (outbox rule 8)?
- **Idempotency**: which natural key dedupes retries (provider message ID, payment reference)?
- **Compliance**: PII fields, retention period, audit requirements, consent.
- **Failure policy**: what happens on provider timeout, duplicate, out-of-order or late events?
- **Volume**: peak events per second, so partitioning/retention decisions are made up front.

---

## 3. Worked Example: WhatsApp Payments

### 3.1 Scenario

1. A merchant (authenticated user) creates a **payment request** for an amount for a customer's phone number.
2. The system sends the customer a WhatsApp message with a pay link (or an in-chat order/payment message).
3. The customer pays via the payment provider (mobile money, card, or WhatsApp-native payments, depending on region).
4. The provider and Meta send **webhooks**: message status (sent/delivered/read/failed) and payment result.
5. The system reconciles state, notifies the merchant, and exposes status via API.

### 3.2 Package map

```text
internal/
  payments/                 FEATURE: payment requests, state machine, ledger entries
    payments.go             entities, states, rules, domain errors (stdlib + uuid only)
    service.go              use-cases + consumer-side interfaces (Repository, Messenger, Charger)
    repo.go                 sqlc + store.InTx + queue InsertTx
    handler.go              httpx.Register operations
    jobs.go                 send-message, expire-request, reconcile workers
  integrations/
    whatsapp/               INTEGRATION: Meta WhatsApp Cloud API
      payload.go            webhook JSON types, ParseEvent, ExternalID
      verify.go             X-Hub-Signature-256 (HMAC SHA-256) + GET verify-token handshake
      client.go             SendTemplate / SendText via platform/outbound
    psp/                    INTEGRATION: payment provider (name is project-specific)
      payload.go verify.go client.go
```

`payments` never imports `whatsapp` or `psp`. `internal/app` adapts them to the interfaces
that `payments.Service` declares.

### 3.3 Domain model (`payments.go`)

```go
package payments

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending   Status = "pending"    // created, message not yet sent
	StatusSent      Status = "sent"       // WhatsApp message accepted by Meta
	StatusAwaiting  Status = "awaiting"   // customer opened link / started payment
	StatusPaid      Status = "paid"       // terminal
	StatusFailed    Status = "failed"     // terminal
	StatusExpired   Status = "expired"    // terminal
	StatusCancelled Status = "cancelled"  // terminal
)

var (
	ErrNotFound          = errors.New("payments: not found")
	ErrInvalid           = errors.New("payments: invalid request")
	ErrIllegalTransition = errors.New("payments: illegal status transition")
	ErrDuplicate         = errors.New("payments: duplicate reference")
)

// transitions is the only definition of the state machine. Terminal states have none.
var transitions = map[Status][]Status{
	StatusPending:  {StatusSent, StatusFailed, StatusCancelled, StatusExpired},
	StatusSent:     {StatusAwaiting, StatusPaid, StatusFailed, StatusCancelled, StatusExpired},
	StatusAwaiting: {StatusPaid, StatusFailed, StatusExpired},
}

type Request struct {
	ID           uuid.UUID
	MerchantID   uuid.UUID
	Reference    string // merchant-supplied, unique per merchant
	AmountMinor  int64  // integer minor units
	Currency     string // ISO 4217
	CustomerHash string // HMAC of E.164 phone, for lookups without storing it in clear
	Status       Status
	ExpiresAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (r Request) CanMoveTo(next Status) bool { /* look up transitions */ return false }
```

Rules to keep in `payments.go`: amount > 0, currency allowlist, expiry bounds, E.164 validation,
the `CanMoveTo` check. A transition applied twice with the same target is a **no-op, not an error**
(webhooks are at-least-once).

### 3.4 Schema (expand-only, with `Down`)

```sql
-- +goose Up
CREATE TABLE payment_requests (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   uuid        NOT NULL REFERENCES users (id),
    reference     text        NOT NULL,
    amount_minor  bigint      NOT NULL CHECK (amount_minor > 0),
    currency      char(3)     NOT NULL,
    customer_hash bytea       NOT NULL,
    customer_enc  bytea       NOT NULL,            -- phone, app-level encrypted
    status        text        NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','sent','awaiting','paid','failed','expired','cancelled')),
    provider_ref  text,
    wa_message_id text,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, reference)
);
CREATE INDEX payment_requests_merchant_idx ON payment_requests (merchant_id, created_at DESC, id);
CREATE UNIQUE INDEX payment_requests_provider_ref_key ON payment_requests (provider_ref) WHERE provider_ref IS NOT NULL;
CREATE UNIQUE INDEX payment_requests_wa_msg_key ON payment_requests (wa_message_id) WHERE wa_message_id IS NOT NULL;
CREATE INDEX payment_requests_expiry_idx ON payment_requests (expires_at) WHERE status IN ('pending','sent','awaiting');

-- Append-only audit of every transition. Never UPDATE or DELETE.
CREATE TABLE payment_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id  uuid        NOT NULL REFERENCES payment_requests (id),
    from_status text,
    to_status   text        NOT NULL,
    cause       text        NOT NULL,   -- 'api','wa_webhook','psp_webhook','expiry'
    inbound_id  bigint,                 -- inbound_events.id when webhook-driven
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_events_request_idx ON payment_events (request_id, id);

-- +goose Down
DROP TABLE IF EXISTS payment_events;
DROP TABLE IF EXISTS payment_requests;
```

If you need a real ledger (balances, payouts, refunds), add a **double-entry** table
(`ledger_entries`: debit/credit, `amount_minor`, `currency`, `request_id`, sum-to-zero check in the
service) in a later migration. Do not mutate a balance column in place.

### 3.5 Service (`service.go`): interfaces defined here

```go
type Repository interface {
	// CreateWithEvent inserts the request and enqueues SendRequestArgs in ONE transaction.
	CreateWithEvent(ctx context.Context, in NewRequest) (Request, error)
	Get(ctx context.Context, id uuid.UUID) (Request, error)
	// Transition applies a status change, writes a payment_events row, and
	// (when to == paid) enqueues the merchant notification, all in ONE transaction.
	// Row is locked (SELECT ... FOR UPDATE) so concurrent webhooks serialise.
	Transition(ctx context.Context, id uuid.UUID, to Status, cause string, inboundID int64) (Request, error)
	// ListByMerchant is keyset-paginated on (created_at DESC, id). Random UUIDs are not
	// time-ordered, so the cursor carries both values; never page on id alone.
	ListByMerchant(ctx context.Context, merchant uuid.UUID, after Cursor, limit int) ([]Request, error)
}

type Messenger interface { // implemented in app by an adapter over integrations/whatsapp
	SendPaymentRequest(ctx context.Context, idempotencyKey string, to string, r Request) (messageID string, err error)
}

type Charger interface { // implemented over integrations/psp
	CreateCheckout(ctx context.Context, idempotencyKey string, r Request) (checkoutURL, providerRef string, err error)
}
```

Authorization (rule 14): `Get`/`List` return `ErrNotFound` for another merchant's request;
only the owner or an admin may cancel. Webhook-driven transitions run as a system actor and are
validated by state machine and provider reference match, not by user identity.

### 3.6 Flows

**A. Create (API, authenticated, `Idempotency-Key` required)**

```text
POST /v1/payments {reference, amount_minor, currency, phone}
  -> auth.RequireAuth -> idempotency.Middleware -> handler -> Service.Create
  -> Repository.CreateWithEvent: INSERT payment_requests + River InsertTx(SendRequestArgs)  [one tx]
  <- 202 {id, status:"pending"}
```

**B. Send (worker)** `payments.send_request`

1. Load request; if status is not `pending`, return nil (idempotent).
2. `Charger.CreateCheckout` keyed by `request.ID`, then store `provider_ref`.
3. `Messenger.SendPaymentRequest` keyed by `request.ID`, then store `wa_message_id`, `Transition(sent)`.
4. Provider 4xx (not 429) returns `river.JobCancel` and `Transition(failed)`; 5xx/429/timeouts return the error so River retries with backoff.

**C. Inbound webhooks (platform/webhook, already built)**

```text
POST /v1/webhooks/whatsapp   Meta: HMAC SHA-256 of raw body, header X-Hub-Signature-256
GET  /v1/webhooks/whatsapp   Meta verification handshake (hub.mode / hub.verify_token / hub.challenge)
                             NOT provided by platform/webhook (Ingest is POST-only); add a small
                             handler in the integration and register it in app
POST /v1/webhooks/psp        provider-specific signature
```

- Register each as a `webhook.Source` (see `acme.WebhookSource`) in `internal/app/api.go`.
  `ExternalID` = Meta `wamid` + status (a message has several status events) or PSP event ID.
- `inbound_events (source, external_id)` unique key gives dedupe for free; ingestion returns 200 fast.
- A `webhook.Processor` per source runs in the River worker and calls `payments.Service.Apply...`.
  Processors map vendor payload to a **domain command** (`MarkPaid(providerRef, amount, currency)`);
  they do not contain business rules.
- **Verify, never trust**: on a "paid" event, check amount and currency equal the request and, for
  high-value flows, call `Charger.GetPayment` to confirm server-side before `Transition(paid)`.
- Out-of-order events: the state machine rejects illegal transitions; the processor logs and
  acknowledges (returns nil) rather than retrying forever.

**D. Expiry (periodic River job)** `payments.expire` (register through `queue.WorkerConfig.PeriodicJobs`; River elects one leader, so replicas do not double-enqueue): selects rows with `expires_at < now()` in
non-terminal status via the partial index, in batches, transitions to `expired`.

**E. Notify merchant** `payments.notify_merchant`: outbound merchant webhook (signed by you,
`Idempotency-Key` = event ID) through `platform/outbound`.

### 3.7 WhatsApp-specific constraints (design for these)

| Constraint | Consequence |
|---|---|
| 24-hour customer service window; outside it only **pre-approved templates** may be sent | Payment requests use a template message; keep template names/IDs in config, not code |
| Per-number throughput and tier limits; 429s | Use `outbound.Config.Limiter` + `Breaker`; queue concurrency tuned per River queue |
| Webhooks can duplicate, reorder, and arrive late | Dedupe by `external_id`; state machine; never assume order |
| Meta requires a quick 200 on webhooks | Already satisfied: persist + enqueue, process async |
| Phone numbers are PII | Encrypt at rest, store lookup hash, redact in logs, define retention |
| Opt-in/consent and STOP handling | Add a `contacts` consent table before sending marketing-adjacent content |
| Access tokens rotate | Load from secret manager; client fetches via a provider interface, not a constant |

### 3.8 Configuration additions

```text
WHATSAPP_PHONE_NUMBER_ID=
WHATSAPP_ACCESS_TOKEN=            # secret; redacted in config LogValue
WHATSAPP_APP_SECRET=              # HMAC key for X-Hub-Signature-256
WHATSAPP_VERIFY_TOKEN=            # GET handshake
WHATSAPP_TEMPLATE_PAYMENT_REQUEST=
PSP_BASE_URL= PSP_API_KEY= PSP_WEBHOOK_SECRET=
PHONE_ENCRYPTION_KEY=             # app-level encryption of customer_enc
PHONE_HASH_KEY=                   # HMAC key for customer_hash
PAYMENT_REQUEST_TTL=24h
```

Add all secrets to `Config.LogValue` redaction and validation (`errors.Join`).

### 3.9 Test plan

| Layer | What | How |
|---|---|---|
| Domain | transition table, amount/currency/E.164 validation | table-driven, no deps |
| Service | ownership, create, double-`paid` is a no-op, illegal transition rejected | fake `Repository`/`Messenger`/`Charger` |
| Integration (`//go:build integration`) | repo atomicity (tx rollback leaves no job), concurrent `Transition` serialises, unique constraints | `storetest` |
| Webhook | bad signature gives 401 and no write; duplicate gives 200 and no second job; sign with `Sign()` helper | `webhook_integration_test.go` pattern |
| Workers | retry vs cancel classification; idempotent re-run after partial success | River test helpers + fakes |
| Contract | OpenAPI route coverage | `routes_test.go` + `make openapi-check` |

---

## 4. Reusable Feature Checklist

Copy into the PR description.

- [ ] Event-storm table completed (section 2)
- [ ] State machine documented in `<feature>.go`; terminal states explicit
- [ ] Migration with working `Down`, `IF NOT EXISTS` for extensions, expand-only
- [ ] Money in integer minor units; time in `timestamptz` UTC
- [ ] Unique keys for every dedupe path (reference, provider ID, webhook external ID)
- [ ] Business write + event in one transaction (`store.InTx` + `InsertTx`)
- [ ] Workers idempotent and classify errors: retry vs `river.JobCancel`
- [ ] Authorization enforced in the service, with a test
- [ ] Write endpoints require `Idempotency-Key`
- [ ] Inputs size-capped, typed, unknown fields rejected
- [ ] Webhooks verify HMAC (`hmac.Equal`) before any write
- [ ] No secrets or PII in logs/errors; new config fields redacted
- [ ] Keyset pagination on list endpoints
- [ ] Metrics and alerts defined (section 9)
- [ ] `make openapi` run; ADR written for any new dependency or deviation
- [ ] `make lint arch-check test test-integration migrate-verify openapi-check` pass

---

## 5. Cross-Cutting Domain Patterns

| Concern | Pattern in this template | Notes |
|---|---|---|
| Atomic event publishing | Transactional outbox via River `InsertTx` | Preferred over dual writes |
| Retries | River backoff; `outbound` retries only idempotent requests | Pass `Idempotency-Key` to vendors |
| Concurrency on one aggregate | `SELECT ... FOR UPDATE` inside `InTx`, or optimistic `version` column | Pick one per aggregate |
| Long workflows | State machine + periodic reconciler | Reach for a saga/workflow engine only when a flow has more than about 5 steps with compensation |
| Reconciliation | Daily job comparing local state to provider reports | Mandatory for money flows |
| Scheduled work | River periodic jobs | Never bare goroutines |
| Soft deletion/retention | Retention job per table | Define TTL per PII table |

---

## 6. Production Readiness Status

Status as of template 0.4.1. Each open item needs an ADR if adopted.

**Done in the template**

| Area | Where |
|---|---|
| Kustomize manifests, separate api and worker with independent HPA, PDB, probes, NetworkPolicy | `deployments/k8s` |
| Worker health listener (`WORKER_HEALTH_ADDR`) | `internal/app/worker.go` |
| Alert rules | `deployments/observability` |
| Image scan, SBOM, `govulncheck`, pinned tools | `.github/workflows/ci.yml`, `make vuln` |
| DB session guards (`DB_STATEMENT_TIMEOUT`, `DB_LOCK_TIMEOUT`, `DB_IDLE_IN_TX_TIMEOUT`) | `store.PoolConfig` |
| Periodic jobs and leader-elected hourly retention (`idempotency_keys`, `refresh_tokens`, `inbound_events`) | `queue.WorkerConfig.PeriodicJobs`, `internal/app/retention.go` |
| UTC timestamp migrations (`make new-feature`, `make new-migration`) | `Makefile`, `cmd/scaffold`, `docs/adr/0008-retention-migrations-and-enterprise-auth.md` |
| Coverage floor | `make cover` |

**Open: platform gaps found in review**

| Priority | Gap | Suggestion |
|---|---|---|
| P0 | No secret manager integration | Inject from Vault/AWS/GCP at deploy; write a rotation runbook |
| P0 | No key rotation for `AUTH_TOKEN_KEY` or webhook secrets (a rotation signs everyone out) | Key ID with two active keys; accept old and new secrets during a window |
| P0 | No backup, PITR or restore drill documented | Managed Postgres with PITR; quarterly restore test |
| P0 | Webhook replay protection beyond dedupe | Optional timestamp tolerance where the provider signs a timestamp |
| P1 | Rate limiter is in-process | Adopt `shared-ratelimit` before running more than one replica |
| P1 | No audit log feature | Append-only `audit_events` for admin and security actions |
| P1 | PII/field-level encryption helper | A platform package once two features need it (rule: not before) |
| P2 | Tenancy | `tenancy` module before the first multi-tenant customer; retrofitting is costly |
| P2 | Cache | None needed yet; add Redis only with measured need |
| P2 | Table growth | Partition `inbound_events`, `payment_events` and `idempotency_keys` by time at tens of millions of rows; retention jobs first |
| P2 | Read scaling | Read replica routing for list and reporting endpoints; keep writes and webhooks on primary |
| P2 | Load test | Promote `perf/smoke.js` to a nightly threshold check |
| P2 | Kill switches | Per-integration switches for outbound calls |
| P3 | Dead-letter handling | River UI for discarded jobs and a replay runbook |
| P3 | Notifications abstraction | Only when a second channel (SMS/email) appears |

---

## 7. Scaling Notes for This Domain

- **API**: stateless; scale horizontally. `DATABASE_URL` behind PgBouncer (transaction mode, `default_query_exec_mode=cache_describe`, ADR 0004).
- **Workers**: scale `worker` independently. Split River queues (`payments_send`, `webhooks`, `default`) so a slow provider cannot starve webhook processing. Cap per-queue concurrency to the vendor's rate limit.
- **Postgres**: queue and business data share one database. Watch connection count, autovacuum on River tables, and job insertion rate. Move to a dedicated queue DB only when measured contention appears.
- **Hot rows**: one payment request is updated by few writers; the `FOR UPDATE` lock is cheap. Avoid global counters (use `COUNT` from indexed queries or a periodic rollup).

---

## 8. Wiring Cheatsheet (`internal/app`)

1. Parse config; add fields to `internal/config/config.go`, `.env.example`, validation, and `LogValue` redaction.
2. Build integration clients with `outbound.NewClient` (timeout, retries only for idempotent, breaker, limiter).
3. Build adapters satisfying `payments.Messenger` and `payments.Charger`.
4. `payments.NewService(repo, ...)`; register handler via `httpx.Register`.
5. Register webhook sources and processors; register workers (`payments.RegisterWorkers`) in `worker.go`.
6. Add periodic jobs (expiry, reconciliation) via `workerCfg.PeriodicJobs`.
7. `make openapi`.

---

## 9. Operations Checklist per Domain

- **Metrics**: counters for transitions by `from,to,cause`; histogram for create-to-paid latency; gauge for pending-older-than-X.
- **Alerts**: paid-without-notification, stuck in `sent` beyond threshold, webhook signature failures spike, provider breaker open, River queue age.
- **Logs**: `request_id`, `payment_id`, `provider_ref`; never phone numbers, tokens, or signatures.
- **Runbooks**: replay a failed webhook, force-reconcile a payment, rotate WhatsApp token, handle a Meta outage (breaker open, requests expire naturally).
- **Reconciliation**: daily report job; mismatches raise an alert, never auto-correct money.
- **Data lifecycle**: retention schedule for `customer_enc`, `inbound_events` payloads, and `payment_events`.

---

## 10. Running Several Projects From This Template

- **Run the method in section 1 per domain, not per project.** Copy sections 2 and 4 into each domain's PR.
- **Migration numbering.** The initial baseline (`00001`–`00005`) is sequential; all subsequent template and project migrations use UTC timestamp versions (`YYYYMMDDHHMMSS_<name>.sql`) via `make new-feature` or `make new-migration` (see ADR 0008). This prevents collisions when pulling upstream template migrations or merging parallel branches.
- **Enterprise authentication.** Keep `internal/users` + PASETO `v4.local` for standalone products. For enterprise SSO (Entra ID, Okta, Keycloak, Auth0), validate JWTs against the provider's JWKS and map claims into `auth.Principal` so `auth.RequireAuth` and service authorization stay unchanged (ADR 0008).
- **Domain code stays in `project` paths** (`internal/<feature>`, `internal/integrations/<vendor>`). Edit `internal/app` and `internal/config` freely, keeping each domain's wiring in a single contiguous block so `make template-diff` merges stay small.
- **Cross-project reuse.** If two projects need the same integration (for example the payment provider), extract it into its own module and import it. Do not copy it between repositories.
- **Verify the `uuid` and keyset advice above against your list endpoints.** The `orders` example pages on a `bigint` identity; domains with UUID keys must page on `(created_at, id)`.
