# sr-backend-draftea-challenge

Event-driven payment system built with Go, PostgreSQL, RabbitMQ, and Traefik.

The solution implements three distributed workflows:

- `deposit`
- `purchase`
- `refund`

The code is organized as four application services plus infrastructure:

- `saga-orchestrator`
- `payments`
- `wallets`
- `catalog-access`
- `rabbitmq`
- `postgres`
- `traefik`

## Documentation

The current implementation-first documentation lives in [docs](./docs/README.md).

Start here:

- [Architecture](./docs/architecture.md)
- [Services](./docs/services.md)
- [Workflows](./docs/workflows.md)
- [Messaging](./docs/messaging.md)
- [Data Model](./docs/data-model.md)
- [Operations](./docs/operations.md)
- [Testing](./docs/testing.md)
- [Decisions](./docs/decisions.md)
- [Future Improvements](./docs/future-improvements.md)

## Quick Start

```bash
cp .env.example .env
docker compose up -d
```

`make build` is available as an optional local sanity check but is not required;
`docker compose up` uses a multi-stage Docker build internally.

Traefik exposes the HTTP entry point at `http://localhost:80`.

## Common Commands

```bash
make test
make test-integration
make test-observability
make vet
make fmt
make check
```

## Notes

- service entrypoints run their own migrations on startup
- sample bootstrap data for local purchase and refund testing lives in
  [docs/examples/bootstrap_sample_data.sql](./docs/examples/bootstrap_sample_data.sql)
- the payment provider is intentionally mocked for challenge scope
