# Documentation

This directory is the implementation-first reference for the current system.
It describes the solution as it exists in code today.

## Start Here

- [Architecture](./architecture.md)
- [Services](./services.md)
- [Workflows](./workflows.md)
- [Messaging](./messaging.md)
- [Data Model](./data-model.md)
- [Operations](./operations.md)
- [Testing](./testing.md)
- [Decisions](./decisions.md)
- [Future Improvements](./future-improvements.md)

## System Summary

The solution implements three distributed workflows:

- wallet deposits through a mocked external provider
- purchases using wallet balance
- refunds by revoking access and re-crediting the wallet

The runtime is composed of:

- `traefik` as HTTP ingress
- `saga-orchestrator` for long-running workflow coordination
- `payments` for the authoritative transaction ledger and provider integration
- `wallets` for balance and wallet movement consistency
- `catalog-access` for offerings, entitlements, and eligibility checks
- `rabbitmq` for asynchronous commands and outcomes
- `postgres` with one schema per service

## Documentation Philosophy

These docs intentionally focus on:

- ownership boundaries
- workflow behavior
- delivery and idempotency guarantees in challenge scope
- operational usage
- the reasoning behind the chosen tradeoffs

They also call out what is deliberately simplified, so the system is explained
honestly rather than as a production-complete payment platform.
