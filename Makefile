SHELL := /bin/bash

# Terminal styling
BLUE   := \033[0;34m
GREEN  := \033[0;32m
YELLOW := \033[1;33m
RED    := \033[0;31m
CYAN   := \033[0;36m
BOLD   := \033[1m
NC     := \033[0m

APP_NAME       ?= {{APP_NAME}}
MIGRATIONS_DIR ?= db/migrations

# Auto-detect container engine: docker compose -> podman compose -> podman-compose
DOCKER_BIN := $(shell command -v docker 2>/dev/null)
PODMAN_BIN := $(shell command -v podman 2>/dev/null)

ifeq ($(DOCKER_BIN),)
  ifneq ($(PODMAN_BIN),)
    COMPOSE ?= podman compose -f deployments/compose.yaml
  else
    COMPOSE ?= docker compose -f deployments/compose.yaml
  endif
else
  COMPOSE ?= docker compose -f deployments/compose.yaml
endif

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

# Local ports and image tags used by the full-stack targets.
API_PORT    ?= 8080
WORKER_PORT ?= 8081
BIN_DIR     ?= bin
APP_IMAGE   ?= app:local
MIGRATE_IMAGE ?= app-migrate:local
APP_COMPOSE  = $(COMPOSE) --profile app
EDGE_COMPOSE = $(COMPOSE) --profile app --profile edge

.PHONY: help up down logs setup dev check build build-images stack stack-edge stack-down stack-logs ps health metrics \
	db-shell db-backup down-volumes clean prune stats logs-api logs-worker migrate migrate-verify sqlc run-api run-worker seed-admin \
	test test-integration lint arch-check vuln new-feature new-migration init strip-examples \
	template-diff add-module perf scaffold-smoke openapi openapi-check cover

help: ## List targets
	@echo -e ""
	@echo -e "$(BLUE)$(BOLD)== $(APP_NAME) control plane ==$(NC)"
	@echo -e ""
	@echo -e "$(CYAN)$(BOLD)Workflows:$(NC)"
	@echo -e "  $(GREEN)make setup$(NC)   one-click: .env, Postgres, migrations, admin seed"
	@echo -e "  $(GREEN)make dev$(NC)     Postgres + migrations, then run the API on :$(API_PORT)"
	@echo -e "  $(GREEN)make stack$(NC)   build images and run Postgres + migrate + api + worker in containers"
	@echo -e "  $(GREEN)make check$(NC)   QA gate: lint, arch-check, tests, openapi-check"
	@echo -e ""
	@echo -e "$(YELLOW)$(BOLD)All targets:$(NC)"
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}'
	@echo -e ""

setup: ## One-click bootstrap: .env, Postgres, migrations, admin seed
	@if [ ! -f .env ]; then echo -e "$(YELLOW)Creating .env from .env.example...$(NC)"; cp .env.example .env; fi
	@echo -e "$(BLUE)1/3 Starting Postgres...$(NC)"
	@$(MAKE) --no-print-directory up
	@echo -e "$(BLUE)2/3 Applying migrations...$(NC)"
	@$(MAKE) --no-print-directory migrate
	@echo -e "$(BLUE)3/3 Seeding admin...$(NC)"
	@$(MAKE) --no-print-directory seed-admin
	@echo -e "$(GREEN)$(BOLD)Ready.$(NC) Run $(CYAN)make run-api$(NC) and $(CYAN)make run-worker$(NC), or $(CYAN)make stack$(NC) for containers."
	@echo -e "  $(CYAN)API docs:$(NC) http://localhost:$(API_PORT)/docs   $(CYAN)Health:$(NC) http://localhost:$(API_PORT)/readyz"

dev: ## Daily dev: Postgres + migrations, then run the API in the foreground
	@$(MAKE) --no-print-directory up
	@$(MAKE) --no-print-directory migrate
	@$(MAKE) --no-print-directory run-api

check: ## QA gate: lint, arch-check, unit tests, openapi-check
	@$(MAKE) --no-print-directory lint
	@$(MAKE) --no-print-directory arch-check
	@$(MAKE) --no-print-directory test
	@$(MAKE) --no-print-directory openapi-check
	@echo -e "$(GREEN)$(BOLD)All QA checks passed.$(NC)"

build: ## Compile api, worker, seed into ./bin
	@echo -e "$(BLUE)Building binaries into $(BIN_DIR)/...$(NC)"
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/worker ./cmd/worker
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/seed ./cmd/seed
	@echo -e "$(GREEN)Built $(BIN_DIR)/{api,worker,seed}$(NC)"

build-images: ## Build the runtime and migrate container images
	@echo -e "$(BLUE)Building container images...$(NC)"
	$(APP_COMPOSE) build
	@echo -e "$(GREEN)Images built: $(APP_IMAGE), $(MIGRATE_IMAGE)$(NC)"

stack: ## Build images and run Postgres, migrations, api, worker in containers
	$(APP_COMPOSE) up -d --build --wait api worker
	@echo -e "$(GREEN)Stack running.$(NC) $(CYAN)API docs:$(NC) http://localhost:$(API_PORT)/docs  $(CYAN)Worker probes:$(NC) http://localhost:$(WORKER_PORT)/readyz"

stack-edge: ## Run full stack with Traefik and Cloudflare Tunnel (requires CLOUDFLARE_TUNNEL_TOKEN)
	@$(LOADENV) : "$${CLOUDFLARE_TUNNEL_TOKEN:?CLOUDFLARE_TUNNEL_TOKEN is required in .env or environment}"; \
	$(EDGE_COMPOSE) up -d --build --wait api worker traefik cloudflared
	@echo -e "$(GREEN)Edge stack running (Cloudflare Tunnel -> Traefik -> API).$(NC)"

stack-down: ## Stop the full container stack (keeps data volume)
	$(EDGE_COMPOSE) down

stack-logs: ## Tail api and worker container logs
	$(APP_COMPOSE) logs -f api worker

logs-api: ## Tail api container logs
	$(APP_COMPOSE) logs -f api

logs-worker: ## Tail worker container logs
	$(APP_COMPOSE) logs -f worker

ps: ## List stack containers
	$(APP_COMPOSE) ps

health: ## Check API and worker readiness over HTTP
	@curl -fsS http://localhost:$(API_PORT)/readyz >/dev/null && echo -e "$(GREEN)API ready$(NC)" || echo -e "$(RED)API not ready on :$(API_PORT)$(NC)"
	@curl -fsS http://localhost:$(WORKER_PORT)/readyz >/dev/null && echo -e "$(GREEN)Worker ready$(NC)" || echo -e "$(YELLOW)Worker not ready on :$(WORKER_PORT)$(NC)"

metrics: ## Fetch Prometheus metrics from the API
	@curl -s http://localhost:$(API_PORT)/metrics

db-shell: ## Open psql on the local Postgres container
	$(COMPOSE) exec postgres psql -U $(PG_USER) -d app

db-backup: ## Dump the local database to backups/ (gzip)
	@mkdir -p backups
	$(COMPOSE) exec -T postgres pg_dump -U $(PG_USER) app | gzip -9 > backups/db_$$(date +%Y%m%d_%H%M%S).sql.gz
	@echo -e "$(GREEN)Backup saved in backups/$(NC)"

down-volumes: ## Stop everything and DESTROY the local database volume
	@echo -e "$(RED)$(BOLD)This deletes the local database volume.$(NC)"
	@read -p "Proceed? [y/N] " -n 1 -r; echo ""; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then $(APP_COMPOSE) down -v; else echo "aborted"; fi

stats: ## Container resource usage snapshot
	@$(if $(DOCKER_BIN),docker,podman) stats --no-stream

clean: ## Remove built binaries
	rm -rf $(BIN_DIR)

prune: ## Prune stopped containers and dangling images
	@$(if $(DOCKER_BIN),docker,podman) system prune -f

up: ## Start local Postgres
	$(COMPOSE) up -d --wait postgres

down: ## Stop local Postgres
	$(COMPOSE) down

logs: ## Tail local Postgres container logs
	$(COMPOSE) logs -f postgres

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

COVER_MIN ?= 70

cover: ## Unit + integration coverage across internal/, fails below COVER_MIN percent
	go test -race -count=1 -tags=integration -coverpkg=./internal/... -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | awk -v min=$(COVER_MIN) '/^total:/ {gsub("%","",$$3); print "coverage: " $$3 "% (min " min "%)"; if ($$3+0 < min) exit 1}'

test-integration: ## Integration tests against real Postgres (testcontainers)
	go test -race -count=1 -tags=integration ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

arch-check: ## Verify platform packages import no feature code
	bash scripts/arch-check.sh

GOVULNCHECK_VERSION ?= v1.9.0

vuln: ## Scan dependencies with govulncheck
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

new-feature: ## Scaffold a feature: make new-feature name=billing
	@test -n "$(name)" || { echo "usage: make new-feature name=<feature>" >&2; exit 1; }
	go run ./cmd/scaffold new-feature --name="$(name)"

new-migration: ## Create a UTC timestamped SQL migration: make new-migration name=add_index
	@test -n "$(name)" || { echo "usage: make new-migration name=<migration_name>" >&2; exit 1; }
	@f="$(MIGRATIONS_DIR)/$$(date -u +%Y%m%d%H%M%S)_$(name).sql"; \
	printf '%s\n\n%s\n' '-- +goose Up' '-- +goose Down' > "$$f"; \
	echo "Created: $$f"

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

openapi: ## Generate api/openapi.yaml from Huma models and route definitions
	go run ./cmd/openapi -o api/openapi.yaml

openapi-check: ## Verify api/openapi.yaml is up to date and generation is deterministic
	@tmp1=$$(mktemp); tmp2=$$(mktemp); \
	trap 'rm -f "$$tmp1" "$$tmp2"' EXIT; \
	go run ./cmd/openapi -o "$$tmp1" && \
	go run ./cmd/openapi -o "$$tmp2" && \
	cmp -s "$$tmp1" "$$tmp2" || { echo "openapi generation is non-deterministic" >&2; exit 1; }; \
	diff -u api/openapi.yaml "$$tmp1" || { echo "api/openapi.yaml is out of date; run 'make openapi' and commit" >&2; exit 1; }
