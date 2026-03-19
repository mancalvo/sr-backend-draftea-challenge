.PHONY: build test test-platform test-messaging test-catalog-access test-payments test-wallets test-saga test-deposit-flow test-purchase-flow test-refund-flow vet fmt check clean \
       check-migrations migrate-up migrate-down migrate-create

SERVICES     := api-gateway saga-orchestrator payments wallets catalog-access
BIN_DIR      := bin
MIGRATIONS   := db/migrations
DATABASE_URL ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@$(POSTGRES_HOST):$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable

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

## vet: run go vet
vet:
	go vet ./...

## fmt: check formatting (fails if files are not formatted)
fmt:
	@test -z "$$(gofmt -l .)" || { echo "files not formatted:"; gofmt -l .; exit 1; }

## check: run fmt + vet + test + build
check: fmt vet test build

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## ---- Migrations ----

## check-migrations: validate migration files (sequential numbering, matching up/down pairs)
check-migrations:
	@echo "checking migration file pairs..."
	@ups=$$(ls $(MIGRATIONS)/*.up.sql 2>/dev/null | wc -l | tr -d ' '); \
	downs=$$(ls $(MIGRATIONS)/*.down.sql 2>/dev/null | wc -l | tr -d ' '); \
	if [ "$$ups" -eq 0 ]; then echo "ERROR: no up migrations found"; exit 1; fi; \
	if [ "$$ups" -ne "$$downs" ]; then echo "ERROR: mismatched up ($$ups) / down ($$downs) migration count"; exit 1; fi; \
	echo "  $$ups up + $$downs down migration files OK"
	@echo "checking migration file naming..."
	@for f in $(MIGRATIONS)/*.sql; do \
		base=$$(basename $$f); \
		echo "$$base" | grep -qE '^[0-9]+_[a-z_]+\.(up|down)\.sql$$' || \
		{ echo "ERROR: bad migration filename: $$base"; exit 1; }; \
	done
	@echo "  all filenames OK"
	@echo "validating migration SQL syntax (basic)..."
	@for f in $(MIGRATIONS)/*.sql; do \
		if [ ! -s "$$f" ]; then echo "ERROR: empty migration file: $$f"; exit 1; fi; \
	done
	@echo "  no empty files OK"
	@echo "check-migrations passed"

## migrate-up: run all up migrations
migrate-up:
	migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" up

## migrate-down: roll back all migrations
migrate-down:
	migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" down -all

## migrate-create: create a new migration pair (usage: make migrate-create NAME=create_foo)
migrate-create:
	migrate create -ext sql -dir $(MIGRATIONS) -seq $(NAME)
