# sr-backend-draftea-challenge

Event-driven payment system built with Go, PostgreSQL, RabbitMQ, and Traefik.

## Repository Layout

```
cmd/                    Service entry points
  api-gateway/          HTTP reverse proxy (Traefik-backed)
  saga-orchestrator/    Workflow orchestration and command ingress
  payments/             Transaction management and provider integration
  wallets/              Wallet balances and movements
  catalog-access/       Users, offerings, and access records
internal/
  platform/             Shared infrastructure (DB, messaging, HTTP helpers)
  services/             Per-service domain logic
db/
  migrations/           SQL migration files (golang-migrate)
  seeds/                Seed data for local development
deploy/
  traefik/              Traefik configuration
  rabbitmq/             RabbitMQ configuration
test/
  integration/          Integration tests
```

## Prerequisites

- Go 1.22+
- Docker and Docker Compose
- Make

## Quick Start

```bash
# Copy environment config
cp .env.example .env

# Build all service binaries
make build

# Run all checks (fmt, vet, test, build)
make check
```

## Makefile Targets

| Target  | Description                                  |
|---------|----------------------------------------------|
| `build` | Compile all service binaries into `bin/`     |
| `test`  | Run all tests                                |
| `vet`   | Run `go vet`                                 |
| `fmt`   | Check source formatting                      |
| `check` | Run fmt + vet + test + build                 |
| `clean` | Remove build artifacts                       |

## Services

| Service              | Description                                      |
|----------------------|--------------------------------------------------|
| `api-gateway`        | External HTTP entry point via Traefik            |
| `saga-orchestrator`  | Command ingress, saga state, workflow progression|
| `payments`           | Transaction records, status, provider integration|
| `wallets`            | Wallet balances, movements, concurrency safety   |
| `catalog-access`     | Users, offerings, access grant/revoke            |

## Runtime Stack

- **Go** — application language
- **PostgreSQL** — persistent storage
- **RabbitMQ** — async messaging between services
- **Traefik** — API gateway / reverse proxy
- **Docker Compose** — local orchestration
