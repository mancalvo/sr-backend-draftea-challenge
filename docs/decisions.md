# Decisions

## Why Go

Go fits the solution well because the system needs:

- explicit ownership boundaries
- simple deployable binaries
- straightforward concurrency primitives
- readable infrastructure code for HTTP, SQL, and RabbitMQ

The language keeps the solution pragmatic and easy to audit.

## Why `net/http` And `chi`

The HTTP stack is intentionally lightweight:

- `net/http` for the standard server model
- `chi` for clean routing and middleware

This keeps handlers simple and consistent across services without introducing a
heavy framework.

## Why `database/sql` And Explicit SQL

The system has correctness-sensitive write paths:

- wallet debits and credits
- conditional access revoke
- transaction status transitions

Explicit SQL makes locking, uniqueness, and transaction boundaries easy to
reason about. That is more important here than ORM convenience.

## Why RabbitMQ

RabbitMQ matches the problem well because the workflows are command- and
outcome-oriented.

It gives the implementation:

- topic exchanges
- practical queue binding
- retry and dead-letter support
- local Docker friendliness

The current solution does not need a heavier streaming platform.

## Why PostgreSQL

PostgreSQL is a strong fit because the system depends on:

- transactional writes
- row-level locking
- partial unique indexes
- mature SQL tooling

One physical instance with one schema per service keeps the challenge runnable
while still respecting ownership boundaries.

## Why Traefik Instead Of A Custom Gateway Service

The ingress problem in this solution is routing, not business logic.

Traefik is the right tool because it:

- routes external HTTP cleanly
- stays out of domain logic
- is easy to run in Docker Compose

Keeping gateway logic out of Go also reduces one deployable that would add very
little value for this scope.

## Why The Saga Uses Both HTTP And RabbitMQ

Not every step has the same communication needs.

Synchronous internal HTTP is used where the orchestrator needs an immediate
authoritative answer:

- register a transaction
- update a transaction status
- decide whether a purchase or refund can even start

RabbitMQ is used where the workflow can proceed asynchronously:

- wallet mutation
- access mutation
- provider result handling

This split keeps the system simpler and more honest than forcing everything
through one transport just for symmetry.

## Why Payments Owns The Ledger

The transaction ledger is the business-level financial record. Putting it in one
service gives the solution:

- one place for transaction state transitions
- one query surface for transaction status and history
- one place to store provider references and refund lineage

Wallet movements still matter, but they are wallet-local operational records,
not the primary user-facing ledger.

## Why Wallets Own Balance Mutation

Wallet consistency should be enforced where the balance actually changes.

That means:

- row locking lives in `wallets`
- duplicate mutation detection lives in `wallets`
- balance-after truth lives in `wallets`

The orchestrator coordinates the process, but it does not try to simulate the
wallet’s invariants from the outside.

## Why Catalog-Access Groups Users, Offerings, And Access

The challenge is about payment workflows, not about building a large catalog
platform.

Grouping these related supporting concepts into one service is the pragmatic
tradeoff:

- offerings provide authoritative pricing
- access records provide purchase and refund eligibility
- entitlements provide the user-facing read model

## Why The Provider Is Mocked

The goal is to demonstrate the workflow contract and integration boundary, not
to integrate with a real PSP.

The mock provider is enough to show:

- asynchronous provider participation
- idempotent deposit processing
- timeout behavior
- provider reference capture

## Why The Code Uses Layered Service Packages

The current package layout keeps each bounded context internally organized
without turning the repo into a giant generic architecture framework.

That structure:

- keeps business code near the service that owns it
- separates HTTP, consumer, repository, and domain concerns
- keeps adapters concrete
- lets each use case define the interfaces it actually consumes

## Why Some Things Are Intentionally Not Built Yet

The implementation deliberately does not include:

- full inbox/outbox infrastructure
- a runtime-defined workflow engine
- a full reconciliation subsystem
- provider-side refunds
- auth and authorization

Those would add a lot of complexity, but they are not necessary to demonstrate
the core architectural and code-quality goals of this solution.
