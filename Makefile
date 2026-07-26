SHELL := /bin/bash
GO     ?= $(HOME)/.local/sdk/go/bin/go
BUN    ?= $(HOME)/.bun/bin/bun
AIR    ?= $(HOME)/go/bin/air

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/enowdev/antares/internal/version.Version=$(VERSION) \
  -X github.com/enowdev/antares/internal/version.Commit=$(COMMIT) \
  -X github.com/enowdev/antares/internal/version.Date=$(DATE)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	 | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ---- development ------------------------------------------------------------

.PHONY: dev
dev: ## Run backend (Air hot reload) and frontend (Vite HMR) together
	@echo "Antares dev: API :8787 · Web :5173"
	@trap 'kill 0' EXIT INT TERM; \
	 $(MAKE) dev-api & \
	 $(MAKE) dev-web & \
	 wait

.PHONY: dev-api
dev-api: ## Backend with hot reload
	@$(AIR) -c .air.toml

.PHONY: dev-web
dev-web: ## Frontend dev server with HMR
	@cd web && $(BUN) x vite --host 0.0.0.0

.PHONY: install
install: ## Install toolchain deps (no sudo)
	@$(GO) mod download
	@$(GO) install github.com/air-verse/air@latest
	@cd web && $(BUN) install

# ---- build ------------------------------------------------------------------

.PHONY: build
build: build-web ## Build a single binary with the dashboard embedded
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/antares ./cmd/antares
	@echo "built bin/antares ($(VERSION))"

.PHONY: build-web
build-web: ## Build the dashboard into internal/server/dist
	@cd web && $(BUN) run build
	@rm -rf internal/server/dist
	@cp -r web/dist internal/server/dist

.PHONY: build-api
build-api: ## Build the backend only (no dashboard)
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/antares ./cmd/antares

# ---- quality ----------------------------------------------------------------

.PHONY: test
test: ## Run Go tests
	@$(GO) test ./...

.PHONY: vet
vet: ## Run go vet
	@$(GO) vet ./...

.PHONY: typecheck
typecheck: ## Typecheck the frontend
	@cd web && $(BUN) x tsc -b --noEmit

.PHONY: smoke
smoke: build ## Build, serve, and load every dashboard route in a real browser
	@ANTARES_PORT=8799 ANTARES_HOME=$$(mktemp -d) ./bin/antares serve >/tmp/antares-smoke.log 2>&1 & \
	 pid=$$!; \
	 trap "kill $$pid 2>/dev/null" EXIT; \
	 sleep 2; \
	 SMOKE_BASE=http://127.0.0.1:8799 $(BUN) scripts/smoke.mjs

.PHONY: check
check: vet test typecheck ## Run every check (add `make smoke` for the browser pass)

.PHONY: fmt
fmt: ## Format Go sources
	@$(GO) fmt ./...

.PHONY: clean
clean: ## Remove build artefacts
	@rm -rf bin .air web/dist
	@echo "cleaned"

.PHONY: doctor
doctor: ## Diagnose the local setup
	@$(GO) run ./cmd/antares doctor
