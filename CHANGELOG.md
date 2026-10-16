# Changelog

All notable changes to this template are recorded here. The template uses
semantic versioning, recorded in `TEMPLATE_VERSION`. Any change that needs
edits in downstream projects carries an "Upgrade notes" entry.

## [Unreleased]

## [0.4.0]

### Added

- `docs/DOMAIN_BLUEPRINT.md`: method, checklists and a worked WhatsApp payments example for adding business domains.
- `docs/OPERATIONS.md`, `docs/SCALING.md`, ADR 0007, and a GitBook structure (`.gitbook.yaml`, `docs/SUMMARY.md`, `docs/README.md`).
- Worker health listener (`WORKER_HEALTH_ADDR`, default `:8081`) with `/healthz` and `/readyz`.
- Named River queues via `WORKER_QUEUES`; `queue.WorkerConfig.Queues` and `TotalWorkers`.
- Pool tuning: `DB_MAX_CONNS`, `DB_MIN_CONNS`, `DB_MAX_CONN_LIFETIME`, `DB_MAX_CONN_IDLE_TIME`.
- Kustomize manifests (`deployments/k8s`), Prometheus alert rules, and a `migrate` Docker target.
- Makefile: styled `help`, `setup`, `dev`, `check`, `build`, `build-images`, `stack`, `ps`, `health`, `db-shell`, `db-backup` and related targets; compose `app` profile (migrate, river-migrate, api, worker).
- CI: image build, Trivy scan, SBOM, kustomize render; Dependabot configuration.

### Changed

- `store.Pinger` accepts the small `store.Pinging` interface instead of `*pgxpool.Pool`.
- README rewritten for advanced users with a documentation map.

### Upgrade notes

- New variables are optional with safe defaults; copy the new entries from `.env.example`.
- The worker now listens on `:8081` by default. Set `WORKER_HEALTH_ADDR=` to disable, or change the port if it conflicts.
- Replace `{{APP_NAME}}` image names in `deployments/k8s/base/kustomization.yaml` or delete `deployments/k8s` if you do not use Kubernetes.

## [0.3.0]

### Added

- Huma v2 integration in `internal/platform/httpx`: Typed operation registration via `httpx.Register` and `httpx.NewAPI`.
- Code-first OpenAPI 3.1 generation via `cmd/openapi`, `make openapi`, and `make openapi-check`.
- Unified RFC 9457 Problem error model across all client errors with field-level details.
- Suppression of `$schema` injection on response payloads.
- Automated route coverage test in `internal/app/routes_test.go`.
- `huma-boundary` depguard rule enforcing that feature packages never import `github.com/danielgtaylor/huma/v2` directly.
- Micro-benchmark in `internal/platform/httpx/benchmark_test.go` measuring Huma vs bare `net/http`.
- ADR 0006 and `templates/modules/huma/README.md`.

### Upgrade notes

- Downstream projects must port `handler.go` files to use `httpx.Register` and typed input/output structs. Domain logic (`<feature>.go`, `service.go`, `repo.go`) requires zero changes.

## [0.2.0]

### Added

- `cmd/scaffold`: Pure-Go CLI replacing shell scripts for `init`, `strip-examples`, `new-feature`, and `template-diff`.
- `gonew` support: Automatic detection and parameter inheritance when initialized via `gonew`.
- Answers tracking: `.template/answers.json` records initial scaffolding configuration and prevents accidental re-runs.
- Path ownership definitions in `.template/OWNERSHIP` and origin tracking in `.template/origin`.
- 4-way `scaffold-smoke` test matrix (`scripts/scaffold-smoke.sh`) and CI workflow integration.
- `docs/SCAFFOLDING.md` guide and ADR 0005.

### Upgrade notes

- Downstream projects can adopt `cmd/scaffold` by copying `cmd/scaffold/`, `.template/`, and updating `Makefile` targets.

### Added

- Repository skeleton: Go module, Makefile, golangci-lint v2 configuration
  with architecture rules, CI workflow, ignore files, and the architecture
  check script.

### Upgrade notes

None. This is the initial release.
