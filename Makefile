.PHONY: build test test-platform test-messaging test-catalog-access test-payments test-wallets test-saga test-deposit-flow test-purchase-flow test-refund-flow test-observability test-integration vet fmt check clean \
       check-migrations migrate-up migrate-down migrate-create check-compose

SERVICES       := saga-orchestrator payments wallets catalog-access
BIN_DIR        := bin
MIGRATION_DIRS := db/migrations/payments db/migrations/wallets db/migrations/catalogaccess db/migrations/saga
DATABASE_URL   ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@$(POSTGRES_HOST):$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable

## build: compile all service binaries into bin/
build:
	@mkdir -p $(BIN_DIR)
	@for svc in $(SERVICES); do \
		echo "building $$svc..."; \
		go build -o $(BIN_DIR)/$$svc ./cmd/$$svc; \
	done

## test: run all tests
test:
	go test ./...

## test-platform: run platform package tests only
test-platform:
	go test ./internal/platform/...

## test-messaging: run messaging and rabbitmq package tests
test-messaging:
	go test ./internal/platform/messaging/... ./internal/platform/rabbitmq/...

## test-catalog-access: run catalog-access service tests
test-catalog-access:
	go test ./internal/services/catalogaccess/...

## test-payments: run payments service tests
test-payments:
	go test ./internal/services/payments/...

## test-wallets: run wallets service tests
test-wallets:
	go test ./internal/services/wallets/...

## test-saga: run saga-orchestrator service tests
test-saga:
	go test ./internal/services/saga/...

## test-deposit-flow: run deposit workflow tests
test-deposit-flow:
	go test -run TestDepositFlow ./internal/services/saga/...

## test-purchase-flow: run purchase workflow tests
test-purchase-flow:
	go test -run TestPurchaseFlow ./internal/services/saga/...

## test-refund-flow: run refund workflow tests
test-refund-flow:
	go test -run TestRefundFlow ./internal/services/saga/...

## test-observability: run observability baseline checks (health, logging, broker logs)
test-observability:
	go test ./test/observability/... ./internal/platform/health/... ./internal/platform/logging/...

## test-integration: run representative integration tests (purchase, refund, deposit, concurrency)
test-integration:
	go test ./test/integration/...

## vet: run go vet
vet:
	go vet ./...

## fmt: check formatting (fails if files are not formatted)
fmt:
	@test -z "$$(gofmt -l .)" || { echo "files not formatted:"; gofmt -l .; exit 1; }

## check: run all quality gates (fmt, vet, tests, migrations, compose, build)
check: fmt vet test test-integration check-migrations check-compose build

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## ---- Migrations ----

## check-migrations: validate per-service migration files (matching up/down pairs, naming)
check-migrations:
	@for dir in $(MIGRATION_DIRS); do \
		echo "checking $$dir..."; \
		ups=$$(ls $$dir/*.up.sql 2>/dev/null | wc -l | tr -d ' '); \
		downs=$$(ls $$dir/*.down.sql 2>/dev/null | wc -l | tr -d ' '); \
		if [ "$$ups" -eq 0 ]; then echo "ERROR: no up migrations in $$dir"; exit 1; fi; \
		if [ "$$ups" -ne "$$downs" ]; then echo "ERROR: mismatched up ($$ups) / down ($$downs) in $$dir"; exit 1; fi; \
		echo "  $$ups up + $$downs down migration files OK"; \
		for f in $$dir/*.sql; do \
			base=$$(basename $$f); \
			echo "$$base" | grep -qE '^[0-9]+_[a-z_]+\.(up|down)\.sql$$' || \
			{ echo "ERROR: bad migration filename: $$base in $$dir"; exit 1; }; \
		done; \
		echo "  filenames OK"; \
		for f in $$dir/*.sql; do \
			if [ ! -s "$$f" ]; then echo "ERROR: empty migration file: $$f"; exit 1; fi; \
		done; \
		echo "  no empty files OK"; \
	done
	@echo "check-migrations passed"

## migrate-up: run all per-service migrations
migrate-up:
	@for dir in $(MIGRATION_DIRS); do \
		svc=$$(basename $$dir); \
		echo "migrating $$svc..."; \
		migrate -path $$dir -database "$(DATABASE_URL)&x-migrations-table=$${svc}_schema_migrations" up; \
	done

## migrate-down: roll back all per-service migrations
migrate-down:
	@for dir in $(MIGRATION_DIRS); do \
		svc=$$(basename $$dir); \
		echo "rolling back $$svc..."; \
		migrate -path $$dir -database "$(DATABASE_URL)&x-migrations-table=$${svc}_schema_migrations" down -all; \
	done

## migrate-create: create a new migration pair (usage: make migrate-create SVC=payments NAME=add_foo)
migrate-create:
	migrate create -ext sql -dir db/migrations/$(SVC) -seq $(NAME)

## ---- Docker Compose ----

## check-compose: validate docker-compose.yml configuration
check-compose:
	@echo "validating docker-compose.yml..."
	docker compose config -q
	@echo "docker-compose config OK"
	@echo "checking required services..."
	@for svc in postgres rabbitmq traefik saga-orchestrator payments wallets catalog-access; do \
		docker compose config --services | grep -q "^$$svc$$" || \
		{ echo "ERROR: missing service $$svc"; exit 1; }; \
	done
	@echo "  all required services present"
	@echo "checking Traefik config files..."
	@test -f deploy/traefik/traefik.yml || { echo "ERROR: deploy/traefik/traefik.yml not found"; exit 1; }
	@test -f deploy/traefik/dynamic.yml || { echo "ERROR: deploy/traefik/dynamic.yml not found"; exit 1; }
	@echo "  Traefik config files OK"
	@echo "check-compose passed"
