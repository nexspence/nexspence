APP       := nexspence
MODULE    := github.com/nexspence-oss/nexspence
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -s -w -X main.Version=$(VERSION)
BUILD_DIR := ./bin

# ── Development ───────────────────────────────────────────────

.PHONY: run
run: ## Start backend in development mode (hot-reload via air)
	@which air > /dev/null || go install github.com/air-verse/air@latest
	air -c .air.toml

.PHONY: run-simple
run-simple: ## Start backend without hot-reload
	go run ./cmd/server serve --config config.yaml

.PHONY: frontend-dev
frontend-dev: ## Start Vite dev server
	cd frontend && npm run dev

.PHONY: dev
dev: ## Start everything for local dev (needs tmux or run in separate terminals)
	@echo "Run 'make run' in one terminal and 'make frontend-dev' in another"
	@echo "Or: docker compose --profile dev up"

# ── Build ─────────────────────────────────────────────────────

.PHONY: build
build: build-frontend ## Build production binary (includes embedded frontend)
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP) ./cmd/server

.PHONY: build-backend
build-backend: ## Build backend binary only
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP) ./cmd/server

.PHONY: build-frontend
build-frontend: ## Build React frontend
	cd frontend && npm ci && npm run build

.PHONY: docker-build
docker-build: ## Build Docker image
	docker build --build-arg VERSION=$(VERSION) -t nexspence-oss/nexspence:$(VERSION) -t nexspence-oss/nexspence:latest .

# ── Database ──────────────────────────────────────────────────

.PHONY: migrate-up
migrate-up: ## Run all pending migrations
	go run ./cmd/server migrate --direction up

.PHONY: migrate-down
migrate-down: ## Roll back one migration
	go run ./cmd/server migrate --direction down

.PHONY: migrate-status
migrate-status: ## Show migration status
	go run ./cmd/server migrate --direction status

.PHONY: db-up
db-up: ## Start only PostgreSQL via Docker Compose
	docker compose up -d postgres

.PHONY: db-shell
db-shell: ## Open psql shell
	docker compose exec postgres psql -U nexspence nexspence

# ── Docker Compose ────────────────────────────────────────────

.PHONY: up
up: ## Start all services
	docker compose up -d

.PHONY: up-dev
up-dev: ## Start all services including Vite dev server
	docker compose --profile dev up -d

.PHONY: down
down: ## Stop all services
	docker compose down

.PHONY: logs
logs: ## Follow logs
	docker compose logs -f nexspence

# ── Quality ───────────────────────────────────────────────────

.PHONY: test
test: ## Run unit tests
	go test -race -count=1 ./...

.PHONY: test-integration
test-integration: ## Run integration tests (needs Docker)
	go test -race -count=1 -tags=integration ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: lint
lint: ## Run golangci-lint
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format Go and TypeScript code
	gofmt -w .
	cd frontend && npm run format 2>/dev/null || true

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: tidy
tidy: ## Tidy go modules
	go mod tidy

# ── Frontend ──────────────────────────────────────────────────

.PHONY: frontend-install
frontend-install: ## Install npm dependencies
	cd frontend && npm ci

.PHONY: frontend-lint
frontend-lint: ## Lint TypeScript/React code
	cd frontend && npm run lint

# ── Utilities ─────────────────────────────────────────────────

.PHONY: generate
generate: ## Run go generate (mocks, etc.)
	go generate ./...

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) coverage.out coverage.html
	cd frontend && rm -rf dist

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
