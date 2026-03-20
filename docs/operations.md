# Operations

## Prerequisites

- Go `1.25.7`
- Docker
- Docker Compose
- `make`
- `golang-migrate` only if you want to run the manual migration targets

## Quick Start

### 1. Prepare Environment

```bash
cp .env.example .env
```

### 2. Build

```bash
make build
```

### 3. Start The Full Stack

```bash
docker compose up -d
docker compose ps
```

Traefik listens on `http://localhost:80`.

## Services And Ports

| Service | Port | Purpose |
| --- | --- | --- |
| `traefik` | `80` | external HTTP ingress |
| `saga-orchestrator` | `8081` | workflow command ingress |
| `payments` | `8082` | transaction ledger and provider integration |
| `wallets` | `8083` | wallet balance reads and wallet mutation consumers |
| `catalog-access` | `8084` | entitlement reads and prechecks |
| `postgres` | `5432` | persistence |
| `rabbitmq` | `5672`, `15672` | broker and management UI |

## Health Checks

Every service exposes:

- `GET /health`

The response is a plain JSON body with:

- `status`
- `service`
- `checks`

The endpoint returns:

- `200` when the service and dependencies are healthy
- `503` when at least one readiness checker is degraded

## Main API Routes

### Commands

- `POST /deposits`
- `POST /purchases`
- `POST /refunds`

These are accepted by `saga-orchestrator` and return `202 Accepted` when the
workflow is successfully started.

### Reads

- `GET /transactions/{transaction_id}`
- `GET /transactions?user_id=...&limit=...&cursor=...`
- `GET /wallets/{user_id}/balance`
- `GET /users/{user_id}/entitlements`

## Example Command Payloads

### Deposit

```json
{
  "user_id": "11111111-1111-1111-1111-111111111111",
  "amount": 5000,
  "currency": "ARS",
  "idempotency_key": "deposit-001"
}
```

### Purchase

```json
{
  "user_id": "11111111-1111-1111-1111-111111111111",
  "offering_id": "33333333-3333-3333-3333-333333333333",
  "idempotency_key": "purchase-001"
}
```

### Refund

```json
{
  "user_id": "11111111-1111-1111-1111-111111111111",
  "offering_id": "33333333-3333-3333-3333-333333333333",
  "transaction_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  "idempotency_key": "refund-001"
}
```

## Configuration

Important environment variables:

### Infrastructure

- `POSTGRES_HOST`
- `POSTGRES_PORT`
- `POSTGRES_USER`
- `POSTGRES_PASSWORD`
- `POSTGRES_DB`
- `RABBITMQ_HOST`
- `RABBITMQ_PORT`
- `RABBITMQ_USER`
- `RABBITMQ_PASSWORD`

### Service URLs

- `CATALOG_ACCESS_URL`
- `PAYMENTS_URL`

### Timing

- `SAGA_TIMEOUT`
- `TIMEOUT_POLL_INTERVAL`
- `SYNC_HTTP_TIMEOUT`
- `PROVIDER_CHARGE_TIMEOUT`

## Migrations

Service entrypoints run their own migrations on startup.

Manual migration targets are available:

```bash
make migrate-up
make migrate-down
make check-migrations
```

## Sample Data

The repository currently ships schema migrations but no automatic seed runner.

To exercise purchase and refund flows locally, insert a sample user, wallet,
and offering first. A ready-to-run example lives at:

- [docs/examples/bootstrap_sample_data.sql](./examples/bootstrap_sample_data.sql)

## Logs

Logs are structured JSON and include the fields that matter for following a
workflow across services:

- `service`
- `transaction_id`
- `correlation_id`
- `message_id`

## Useful Commands

```bash
make test
make test-integration
make test-observability
make vet
make fmt
make check
docker compose logs -f saga-orchestrator
```
