# Porting Playbook

Use this guide when migrating an existing Go-Kit or Python (Litestar/FastAPI) service into a project scaffolded from `granite-go`.

## Migration Rules

1. **One domain per PR.** Never port two domains in one commit.
2. **Port first, improve later.** Behavior changes require their own commit and an ADR. Collect improvement ideas in `docs/migration/LEDGER.md`.
3. **Preserve API shapes by default.** Breaking changes require an ADR and a versioned route.
4. **Schema changes are expand/contract.** Never drop or rename a column the legacy service still reads.
5. **Contract tests define "same behavior".** Record golden request/response pairs under `tests/contract/<domain>/` before porting.
6. **Strangler fallback is temporary.** `internal/app/legacy.go` proxies unported routes and is deleted in Phase M7.

## Phases (`MODE=migrate`)

| Phase | Work | Output | Gate |
|---|---|---|---|
| **M0 Inventory** | List every domain, endpoint, table, side effect, and external dependency; rank by coupling and risk. | `docs/migration/INVENTORY.md`, `docs/migration/LEDGER.md` | Every endpoint and table accounted for |
| **M1 Baseline** | Import schema into `0000_baseline` goose migration; decide token/password compatibility ADR; wire `internal/app/legacy.go`. | Baseline migration, ADR, `internal/app/legacy.go` | `make migrate-verify` passes; fallback serves legacy routes |
| **M2 Characterize** | Capture golden request/response pairs (including errors and pagination) as contract tests. | `tests/contract/<domain>/` | Contract tests pass against legacy service |
| **M3 Port** | Implement `<feature>.go`, `service.go`, `repo.go`, `handler.go`, `jobs.go` with identical wire shapes. | `internal/<domain>/` | Service unit tests + contract tests pass |
| **M4 Strangle** | Register ported routes on `ServeMux` behind `ENABLED_FEATURES` toggle. | Wiring in `internal/app` | Both implementations pass contract tests; toggle works |
| **M5 Compare** | Mirror read-only traffic in staging and diff responses. | Diff report | No unexplained diffs |
| **M6 Cut over** | Enable in production; monitor error rate and latency; keep toggle for one release. | Ledger row `cut-over` | Error and latency thresholds hold |
| **M7 Decommission** | Delete legacy handlers and `internal/app/legacy.go` once all domains are cut over. | Ledger row `decommissioned` | Build and contract tests green |

## Legacy Stack Mapping

| Legacy concept | Go-Kit | Python (Litestar/FastAPI) | granite-go home |
|---|---|---|---|
| Request/response types | `requests.go`, `responses.go` | `msgspec`/Pydantic models | Typed input/output structs in `handler.go` |
| Routing and decoding | Transport + decoders | Controller decorators | `handler.go` (`httpx.Register` with typed struct tags) |
| Endpoint layer | `Endpoint func(ctx, any) (any, error)` + transport pair | Controller method | Replaced by typed Huma operations calling service use-cases |
| Business logic | Service interface | `services.py` | `service.go` and `<feature>.go` |
| Persistence | sqlc or custom | SQLAlchemy | `repo.go` + sqlc |
| Cross-cutting | Endpoint middleware | Litestar middleware and guards | `httpx.AdaptMiddleware` wrapping standard `http.Handler` middlewares |
| Background work | Often none | SAQ, BackgroundTask | River jobs |
| Outbox | Custom or none | Outbox table + relay | River `InsertTx` in the same transaction |
