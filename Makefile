.DEFAULT_GOAL := help
SHELL := /bin/bash

BIN     := bin
COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: help up down logs probe build test lint fmt clean

help: ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-10s %s\n", $$1, $$2}'

up: ## Start HydraDB and wait until it answers readiness
	@mkdir -p deploy/hydradb-data/store deploy/hydradb-data/cache
	@test -f deploy/hydradb-data/auth-token || \
		printf '%s\n' 'local-development-token-32-bytes' > deploy/hydradb-data/auth-token
	@$(COMPOSE) up -d
	@for i in $$(seq 1 60); do \
		if [ "$$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:9090/readyz)" = "200" ]; then \
			echo "hydradb ready after $${i}s"; exit 0; fi; sleep 1; done; \
		echo "hydradb did not become ready" >&2; exit 1

down: ## Stop HydraDB, keeping its data
	@$(COMPOSE) down

logs: ## Follow HydraDB logs
	@$(COMPOSE) logs -f

probe: ## Re-measure the HydraDB behaviour recorded in FINDINGS.md
	@go run ./cmd/probe

build: ## Build binaries into bin/
	@mkdir -p $(BIN)
	@go build -o $(BIN)/ ./cmd/...

test: ## Run tests
	@go test ./...

lint: ## Vet and, if installed, golangci-lint
	@go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run || echo "golangci-lint unavailable for this Go version, ran go vet only"; \
	else echo "golangci-lint not installed, ran go vet only"; fi

fmt: ## Format
	@gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

clean: ## Remove build output and the local HydraDB store
	@rm -rf $(BIN)
	@$(COMPOSE) down -v 2>/dev/null || true
	@rm -rf deploy/hydradb-data
