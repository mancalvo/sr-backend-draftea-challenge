# sr-backend-draftea-challenge

Event-driven payment system built with Go, PostgreSQL, RabbitMQ, and Traefik.

Implements three workflows — **deposit**, **purchase**, and **refund** — using the saga orchestration pattern with async messaging, compensation handling, and concurrency-safe wallet operations.

## Repository Layout

```
cmd/                          Service entry points (one main per service)
  api-gateway/                HTTP reverse proxy (Traefik-backed)
  saga-orchestrator/          Workflow orchestration and command ingress
  payments/                   Transaction management and provider integration
  wallets/                    Wallet balances and movements
  catalog-access/             Users, offerings, and access records
internal/
  platform/                   Shared infrastructure
    config/                   Environment variable loading
    health/                   Health check handler and checkers
    httpx/                    JSON response helpers, middleware, strict decoder
    logging/                  Structured logging with context keys
    messaging/                Message envelope, typed contracts, routing constants
    rabbitmq/                 RabbitMQ connection, topology, publisher, consumer
  services/                   Per-service domain logic
    saga/                     Saga state machine, handler, consumer, timeout poller
    payments/                 Transaction models, repository, provider abstraction
    wallets/                  Wallet models, repository, atomic debit/credit
    catalogaccess/            User/offering models, access grant/revoke, prechecks
db/
  migrations/                 SQL migration files (golang-migrate)
  seeds/                      Seed data for local development
deploy/
  traefik/                    Traefik static and dynamic configuration
  rabbitmq/                   RabbitMQ configuration (placeholder)
test/
  integration/                Representative cross-service workflow tests
  observability/              Health, logging, and broker baseline tests
```

## Architecture-to-Code Mapping

### Saga Orchestration

The saga-orchestrator owns workflow progression. It receives commands via HTTP (deposits, purchases, refunds), creates saga instances, and drives the workflow by publishing commands and reacting to outcome events.

| Concept                  | Code Location                                    |
|--------------------------|--------------------------------------------------|
| Saga state machine       | `internal/services/saga/models.go`               |
| Legal transitions        | `internal/services/saga/models.go` (`legalSagaTransitions`) |
| Command ingress (HTTP)   | `internal/services/saga/handler.go`              |
| Outcome event dispatch   | `internal/services/saga/consumer.go`             |
| Saga persistence         | `internal/services/saga/repository.go`           |
| Timeout poller           | `internal/services/saga/timeout.go`              |
| Idempotency at ingress   | `internal/services/saga/handler.go` (`checkIdempotency`) |
| Service clients          | `internal/services/saga/clients.go`              |

### Wallets

The wallets service owns wallet balances and movements with concurrency safety via row-level locking and deduplication.

| Concept                  | Code Location                                    |
|--------------------------|--------------------------------------------------|
| Wallet models            | `internal/services/wallets/models.go`            |
| Atomic debit/credit      | `internal/services/wallets/repository.go`        |
| Dedup by (txn, step)     | `internal/services/wallets/repository.go` (`Debit`/`Credit`) |
| Command handlers         | `internal/services/wallets/consumer.go`          |
| Balance endpoint         | `internal/services/wallets/handler.go`           |

### Payments

The payments service owns transaction records, status transitions, and provider integration.

| Concept                  | Code Location                                    |
|--------------------------|--------------------------------------------------|
| Transaction state machine| `internal/services/payments/models.go`           |
| Transaction repository   | `internal/services/payments/repository.go`       |
| Provider abstraction     | `internal/services/payments/provider.go`         |
| Deposit command handler  | `internal/services/payments/consumer.go`         |
| HTTP endpoints           | `internal/services/payments/handler.go`          |

### Catalog-Access

The catalog-access service owns users, offerings, and access records.

| Concept                  | Code Location                                    |
|--------------------------|--------------------------------------------------|
| Models                   | `internal/services/catalogaccess/models.go`      |
| Access grant/revoke      | `internal/services/catalogaccess/repository.go`  |
| Command handlers         | `internal/services/catalogaccess/consumer.go`    |
| Purchase/refund prechecks| `internal/services/catalogaccess/handler.go`     |

### Messaging

All async communication flows through RabbitMQ with typed message contracts.

| Concept                  | Code Location                                    |
|--------------------------|--------------------------------------------------|
| Message envelope         | `internal/platform/messaging/envelope.go`        |
| Command/outcome payloads | `internal/platform/messaging/contracts.go`       |
| Exchange/queue/routing   | `internal/platform/messaging/routing.go`         |
| Topology declaration     | `internal/platform/rabbitmq/topology.go`         |
| Publisher                | `internal/platform/rabbitmq/publisher.go`        |
| Consumer (retry/ack)     | `internal/platform/rabbitmq/consumer.go`         |

### Database Schema

Each service owns its own PostgreSQL schema with isolated migrations:

| Migration                                    | Schema               | Tables                                  |
|----------------------------------------------|----------------------|-----------------------------------------|
| `000001_create_payments_schema`              | `payments`           | `transactions`                          |
| `000002_create_wallets_schema`               | `wallets`            | `wallets`, `wallet_movements`           |
| `000003_create_catalog_access_schema`        | `catalog_access`     | `users`, `offerings`, `access_records`  |
| `000004_create_saga_orchestrator_schema`     | `saga_orchestrator`  | `saga_instances`, `idempotency_keys`    |

## Workflow Diagrams

### Purchase Flow

```
Client -> POST /purchases -> saga-orchestrator
  1. Validate + purchase precheck (sync to catalog-access)
  2. Register transaction (sync to payments)
  3. Create saga -> publish wallet.debit.requested
  4. wallets: debit wallet -> publish wallet.debited
  5. saga: receive wallet.debited -> publish access.grant.requested
  6. catalog-access: grant access -> publish access.granted
  7. saga: receive access.granted -> complete saga + update transaction
```

**Compensation**: If `access.grant.conflicted` after debit, the saga publishes `wallet.credit.requested` to reverse the debit, completing as `compensated`.

### Deposit Flow

```
Client -> POST /deposits -> saga-orchestrator
  1. Register transaction (sync to payments)
  2. Create saga -> publish payments.deposit.requested
  3. payments: call provider -> publish provider.charge.succeeded
  4. saga: receive charge success -> publish wallet.credit.requested
  5. wallets: credit wallet -> publish wallet.credited
  6. saga: receive wallet.credited -> complete saga + update transaction
```

**Timeout**: If the provider does not respond within the saga timeout, the timeout poller transitions the saga to `timed_out`. Late provider responses can still resume and complete the workflow.

### Refund Flow

```
Client -> POST /refunds -> saga-orchestrator
  1. Validate + refund precheck (sync to catalog-access)
  2. Register transaction (sync to payments)
  3. Create saga -> publish access.revoke.requested
  4. catalog-access: revoke access -> publish access.revoked
  5. saga: receive access.revoked -> publish wallet.credit.requested
  6. wallets: credit wallet -> publish wallet.credited
  7. saga: receive wallet.credited -> complete saga + update transaction
```

## RabbitMQ Topology

| Exchange             | Type   | Purpose                                  |
|----------------------|--------|------------------------------------------|
| `workflow.commands`  | topic  | Commands dispatched to service queues    |
| `workflow.outcomes`  | topic  | Outcome events routed to saga queue      |
| `workflow.dlx`       | fanout | Dead-letter exchange for failed messages |

| Queue                      | Binds To            | Routing Keys                                    |
|----------------------------|---------------------|------------------------------------------------|
| `payments.commands`        | `workflow.commands` | `payments.deposit.requested`                   |
| `wallets.commands`         | `workflow.commands` | `wallet.debit.requested`, `wallet.credit.requested` |
| `catalog_access.commands`  | `workflow.commands` | `access.grant.requested`, `access.revoke.requested` |
| `saga.outcomes`            | `workflow.outcomes` | `wallet.*`, `access.*`, `provider.*`           |

## API Endpoints

### External (via Traefik)

| Method | Path                                   | Service            | Description                   |
|--------|----------------------------------------|--------------------|-------------------------------|
| POST   | `/deposits`                            | saga-orchestrator  | Initiate a deposit            |
| POST   | `/purchases`                           | saga-orchestrator  | Initiate a purchase           |
| POST   | `/refunds`                             | saga-orchestrator  | Initiate a refund             |
| GET    | `/transactions/{transaction_id}`       | payments           | Get transaction by ID         |
| GET    | `/transactions?user_id={user_id}`      | payments           | List user transactions        |
| GET    | `/wallets/{user_id}/balance`           | wallets            | Get wallet balance            |
| GET    | `/users/{user_id}/entitlements`        | catalog-access     | List active entitlements      |

### Internal (service-to-service)

| Method | Path                                          | Service        | Description                   |
|--------|-----------------------------------------------|----------------|-------------------------------|
| POST   | `/internal/transactions`                      | payments       | Register a new transaction    |
| PATCH  | `/internal/transactions/{id}/status`          | payments       | Update transaction status     |
| POST   | `/internal/purchase-precheck`                 | catalog-access | Validate purchase eligibility |
| POST   | `/internal/refund-precheck`                   | catalog-access | Validate refund eligibility   |

## Prerequisites

- Go 1.22+
- Docker and Docker Compose
- Make
- [golang-migrate](https://github.com/golang-migrate/migrate) (for running migrations)

## Quick Start

```bash
# 1. Copy environment config
cp .env.example .env

# 2. Build all service binaries
make build

# 3. Run all checks (formatting, vet, unit tests, integration tests, migrations, compose config)
make check
```

## Running with Docker Compose

```bash
# Start all services (PostgreSQL, RabbitMQ, Traefik, and 4 application services)
docker compose up -d

# Check service health
docker compose ps

# View logs for a specific service
docker compose logs -f saga-orchestrator

# Run database migrations
make migrate-up

# Tear down
docker compose down

# Tear down and remove volumes (clean slate)
docker compose down -v
```

Once running, the API gateway (Traefik) is available at `http://localhost:80` and routes requests to the appropriate backend service.

## Running Tests

```bash
# Run ALL tests (unit + integration + observability)
go test ./...

# Run only unit tests for a specific service
make test-saga
make test-wallets
make test-payments
make test-catalog-access

# Run workflow-specific saga tests
make test-purchase-flow
make test-refund-flow
make test-deposit-flow

# Run integration tests (cross-service workflow scenarios)
make test-integration

# Run observability baseline tests
make test-observability

# Run the full aggregated check (fmt, vet, all tests, migration validation, compose validation, build)
make check
```

## Makefile Targets

| Target               | Description                                                    |
|----------------------|----------------------------------------------------------------|
| `build`              | Compile all service binaries into `bin/`                       |
| `test`               | Run all Go tests (`go test ./...`)                             |
| `test-integration`   | Run representative integration tests                           |
| `test-observability` | Run observability baseline tests                               |
| `test-saga`          | Run saga-orchestrator unit tests                               |
| `test-wallets`       | Run wallets service unit tests                                 |
| `test-payments`      | Run payments service unit tests                                |
| `test-catalog-access`| Run catalog-access service unit tests                          |
| `test-purchase-flow` | Run purchase workflow tests only                               |
| `test-refund-flow`   | Run refund workflow tests only                                 |
| `test-deposit-flow`  | Run deposit workflow tests only                                |
| `vet`                | Run `go vet`                                                   |
| `fmt`                | Check source formatting                                        |
| `check`              | Full quality gate: fmt + vet + test + integration + migrations + compose + build |
| `clean`              | Remove build artifacts                                         |
| `check-migrations`   | Validate migration file pairs and naming                       |
| `check-compose`      | Validate docker-compose.yml and required services              |
| `migrate-up`         | Run all database migrations                                    |
| `migrate-down`       | Roll back all database migrations                              |

## Test Coverage

### Integration Tests (`test/integration/`)

These tests wire together the saga-orchestrator, wallets, catalog-access, and payments service layers using in-memory repositories and a simulated message bus, verifying the full cross-service workflow without requiring Docker or external infrastructure.

| Test                                           | Scenario                                                        |
|------------------------------------------------|-----------------------------------------------------------------|
| `TestIntegration_PurchaseHappyPath`            | Full purchase: debit -> grant access -> saga completed          |
| `TestIntegration_PurchaseInsufficientFunds`    | Debit rejected due to insufficient funds -> saga failed         |
| `TestIntegration_ConcurrentPurchaseSafety`     | Two concurrent purchases, at most one succeeds                  |
| `TestIntegration_DepositTimeoutHandling`       | Saga times out, late provider success resumes and completes     |
| `TestIntegration_RefundHappyPath`              | Full refund: revoke access -> credit wallet -> saga completed   |

### Unit Tests (per-service)

Each service package includes targeted unit tests covering models, handlers, consumers, and repositories. Saga consumer tests include comprehensive workflow flow tests for all three workflows (purchase, refund, deposit) including happy paths, failures, compensation, duplicates, and timeout recovery.

## Services

| Service              | Port | Description                                        |
|----------------------|------|----------------------------------------------------|
| `api-gateway`        | 80   | External HTTP entry point via Traefik              |
| `saga-orchestrator`  | 8081 | Command ingress, saga state, workflow progression  |
| `payments`           | 8082 | Transaction records, status, provider integration  |
| `wallets`            | 8083 | Wallet balances, movements, concurrency safety     |
| `catalog-access`     | 8084 | Users, offerings, access grant/revoke              |

## Runtime Stack

- **Go** — application language
- **PostgreSQL** — persistent storage (one DB, four schemas)
- **RabbitMQ** — async messaging between services
- **Traefik** — API gateway / reverse proxy
- **Docker Compose** — local orchestration

## Key Design Decisions

- **Saga orchestration over choreography**: A central orchestrator (`saga-orchestrator`) drives workflow progression, making the flow explicit and easier to reason about.
- **Sync prechecks, async execution**: Required prechecks (catalog-access) and transaction registration (payments) are synchronous. Wallet operations, access grants, and provider calls are async through RabbitMQ.
- **Compensation over two-phase commit**: When a purchase's access grant conflicts after a successful debit, the saga compensates by crediting the wallet back rather than using distributed locks.
- **Timeout as persisted state**: Saga timeouts are stored in the database and checked by a poller, surviving process restarts. Late events can legally resume a timed-out saga.
- **Idempotency at every boundary**: Ingress idempotency keys, wallet movement dedup by `(transaction_id, source_step)`, catalog-access unique active access constraints, and saga state machine transition validation.
