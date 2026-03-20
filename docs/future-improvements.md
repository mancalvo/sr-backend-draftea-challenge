# Future Improvements

## Runtime-Driven Workflows

The saga code is already separated into:

- workflow definitions
- use cases
- activities

That makes it a good candidate for a future step where workflows are loaded from
configuration or persisted definitions instead of being fully static in Go.

## Full Reconciliation System

Today the system can preserve explicit terminal states such as:

- `timed_out`
- `reconciliation_required`

What it does not yet include is a dedicated reconciliation subsystem that:

- scans unresolved records
- correlates external truth
- applies safe repair actions
- records operator decisions

## Outbox / Inbox Reliability

The current implementation is reliable in challenge scope, but it still does
not provide a full outbox/inbox solution for infrastructure-level dual-write
failures.

If the system were pushed toward production, this would be a major next step.

## Real Provider Adapters

The provider boundary in `payments` is ready for additional implementations.

Likely next steps:

- add a real PSP adapter
- capture richer provider payloads
- persist provider audit events
- formalize provider-specific retry and timeout policies

## Seed And Local Bootstrap Tooling

The repository currently documents sample bootstrap data but does not automate
it yet.

Useful next improvements:

- a `make seed` target
- deterministic local data bootstrap
- scenario-specific sample data for demos

## Authentication And Authorization

The current API trusts provided user identifiers.

Future work would likely include:

- authenticated users
- authorization checks per resource
- service-to-service identity for internal APIs

## Stronger End-To-End Infra Tests

The in-process integration suite is useful and fast, but a future version could
add a small Docker-backed suite that exercises:

- real PostgreSQL behavior
- real RabbitMQ delivery
- startup ordering and health behavior
- migration and routing behavior together

## Cleanup Candidates

A few smaller cleanup tasks remain attractive:

- remove the dormant `created` saga status from the runtime model and schema
- extract repeated starter helper code where it improves clarity
- continue consolidating older design docs so there is less historical drift in
  the workspace
