.PHONY: build test test-platform vet fmt check clean

SERVICES := api-gateway saga-orchestrator payments wallets catalog-access
BIN_DIR  := bin

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
