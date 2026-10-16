APP_NAME       ?= {{APP_NAME}}
MIGRATIONS_DIR ?= db/migrations
COMPOSE        ?= docker compose -f deployments/compose.yaml
EXAMPLES       ?= keep

# Local database defaults, matching deployments/compose.yaml (added in Phase 8.a).
PG_USER   ?= app
PG_PASS   ?= app
PG_HOST   ?= localhost
PG_PORT   ?= 5432
VERIFY_DB ?= migrate_verify
VERIFY_URL ?= postgres://$(PG_USER):$(PG_PASS)@$(PG_HOST):$(PG_PORT)/$(VERIFY_DB)?sslmode=disable
PSQL      ?= $(COMPOSE) exec -T postgres psql -U $(PG_USER) -d postgres

# Loads .env into the environment of a single recipe line.
LOADENV = set -a; if [ -f .env ]; then . ./.env; fi; set +a;

.DEFAULT_GOAL := help

.PHONY: help up down migrate migrate-verify sqlc run-api run-worker seed-admin \
	test test-integration lint arch-check vuln new-feature init strip-examples \
	template-diff add-module perf scaffold-smoke

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

up: ## Start local Postgres
	$(COMPOSE) up -d --wait postgres

down: ## Stop local Postgres
	$(COMPOSE) down

migrate: ## Apply app and River migrations using the direct database URL
	@$(LOADENV) : "$${DATABASE_URL:?DATABASE_URL is required}"; \
	goose -dir $(MIGRATIONS_DIR) postgres "$${DIRECT_DATABASE_URL:-$$DATABASE_URL}" up && \
	go run ./cmd/worker migrate -direction up

migrate-verify: ## Apply up, down, up (app and River) on a throwaway database
	$(PSQL) -c "DROP DATABASE IF EXISTS $(VERIFY_DB)"
	$(PSQL) -c "CREATE DATABASE $(VERIFY_DB)"
	goose -dir $(MIGRATIONS_DIR) postgres "$(VERIFY_URL)" up
	go run ./cmd/worker migrate -direction up -url "$(VERIFY_URL)"
	go run ./cmd/worker migrate -direction down -url "$(VERIFY_URL)"
	goose -dir $(MIGRATIONS_DIR) postgres "$(VERIFY_URL)" down-to 0
	goose -dir $(MIGRATIONS_DIR) postgres "$(VERIFY_URL)" up
	go run ./cmd/worker migrate -direction up -url "$(VERIFY_URL)"
	$(PSQL) -c "DROP DATABASE $(VERIFY_DB)"

sqlc: ## Regenerate typed queries (commit the result)
	sqlc generate

run-api: ## Run the API
	@$(LOADENV) go run ./cmd/api

run-worker: ## Run the worker
	@$(LOADENV) go run ./cmd/worker

seed-admin: ## Create the first admin from SEED_ADMIN_* env
	@$(LOADENV) go run ./cmd/seed

test: ## Unit tests with the race detector
	go test -race -count=1 ./...

test-integration: ## Integration tests against real Postgres (testcontainers)
	go test -race -count=1 -tags=integration ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

arch-check: ## Verify platform packages import no feature code
	bash scripts/arch-check.sh

vuln: ## Scan dependencies with govulncheck
	govulncheck ./...

new-feature: ## Scaffold a feature: make new-feature name=billing
	@test -n "$(name)" || { echo "usage: make new-feature name=<feature>" >&2; exit 1; }
	go run ./cmd/scaffold new-feature --name="$(name)"

init: ## Scaffold a project (once): make init NAME=<app> MODULE=<module> EXAMPLES=keep|strip
	go run ./cmd/scaffold init --name="$(NAME)" --module="$(MODULE)" --examples="$(EXAMPLES)"

strip-examples: ## Remove example code: make strip-examples CONFIRM=yes
	go run ./cmd/scaffold strip-examples $(if $(filter yes,$(CONFIRM)),--confirm,)

template-diff: ## Show upstream template changes: make template-diff TAG=<version>
	go run ./cmd/scaffold template-diff --tag="$(TAG)"

add-module: ## Adopt an opt-in module: make add-module NAME=<module>
	@test -n "$(NAME)" || { echo "usage: make add-module NAME=<module>" >&2; exit 1; }
	@test -f templates/modules/$(NAME)/apply.sh || { echo "module $(NAME) has no apply.sh in this template version" >&2; exit 1; }
	@bash templates/modules/$(NAME)/apply.sh

perf: ## Run the k6 smoke scenario
	@$(LOADENV) k6 run -e BASE_URL="$${BASE_URL:-http://localhost:8080}" perf/smoke.js
 
scaffold-smoke: ## Run 4-way scaffold matrix (fresh/gonew x keep/strip)
	bash scripts/scaffold-smoke.sh
