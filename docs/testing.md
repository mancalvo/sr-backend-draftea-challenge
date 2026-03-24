# Testing

## Verification Commands

The main local verification commands are:

```bash
go test ./...
go vet ./...
gofmt -l .
docker compose config -q
```

The repository also exposes a combined gate:

```bash
make check
```

## Test Layers

## Unit Tests

Unit tests cover:

- HTTP handlers
- RabbitMQ consumers
- repositories
- state machines
- middleware and platform helpers
- workflow definitions

These tests are the main protection for idempotency, state transitions, error
mapping, and service-local invariants.

## Representative Integration Tests

The integration suite lives in `test/integration/workflow_test.go`.

It wires together the real service logic using:

- in-memory repositories
- a queued in-process bus that behaves like broker dispatch
- a configurable mock provider

Covered scenarios include:

- purchase happy path
- purchase insufficient funds
- concurrent purchase safety
- deposit timeout handling
- refund happy path

This suite is intentionally representative rather than a full infrastructure
end-to-end environment.

## Observability Tests

The observability suite checks:

- health endpoints (per-service and readiness checker behavior)
- structured logging keys
- broker publish and consume logging
- middleware correlation ID propagation

## Why This Test Strategy Is Appropriate

The goal of the solution is to demonstrate:

- clean boundaries
- reliable workflow handling
- concurrency safety
- challenge-scope resilience

The current test pyramid supports that well without turning the repository into
an infrastructure-heavy setup for every single run.

## Honest Limitations

- the integration suite is in-process, not Docker-backed
- there is no full broker crash/recovery or database failover test matrix
- the provider adapter is mocked

These are acceptable tradeoffs for the scope of the challenge, and the docs
should present them explicitly rather than implying production-grade coverage.
