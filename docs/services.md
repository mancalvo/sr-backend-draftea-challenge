# Services

## Saga Orchestrator

### Responsibility

- Accept `deposit`, `purchase`, and `refund` commands.
- Persist saga state and command-ingress idempotency.
- Sequence each workflow step.
- React to outcome events.
- Trigger compensation and timeout resolution.

### What It Does Not Own

- wallet balance
- offering price
- access state
- provider logic
- transaction-state authority

### Public HTTP

- `POST /deposits`
- `POST /purchases`
- `POST /refunds`
- `GET /health`

### Internal Dependencies

- `payments` over HTTP for transaction registration and status updates
- `catalog-access` over HTTP for purchase and refund prechecks
- RabbitMQ for workflow progression

### Internal Structure

- `api/` command ingress
- `usecases/startdeposit`
- `usecases/startpurchase`
- `usecases/startrefund`
- `usecases/handleoutcome`
- `usecases/timeout`
- `usecases/idempotency`
- `workflows/` static workflow definitions
- `activities/` command publishing and ledger update helpers

## Payments

### Responsibility

- Own the authoritative transaction ledger.
- Register new transactions.
- Validate and persist transaction status transitions.
- Serve transaction reads and transaction history.
- Process deposit commands through the provider adapter.

### Public HTTP

- `GET /transactions/{transaction_id}`
- `GET /transactions?user_id=...&limit=...&cursor=...`
- `GET /health`

### Internal HTTP

- `POST /internal/transactions`
- `PATCH /internal/transactions/{transaction_id}/status`

### Consumed Commands

- `payments.deposit.requested`

### Published Outcomes

- `provider.charge.succeeded`
- `provider.charge.failed`

### Internal Structure

- `domain/` transaction model and legal transitions
- `service/` reusable ledger operations
- `usecases/processdeposit/` provider-driven deposit handling
- `provider/mock.go` for the challenge-scope provider adapter

## Wallets

### Responsibility

- Own wallet balances.
- Apply debits and credits atomically.
- Persist wallet movements as the local journal of applied balance changes.
- Enforce idempotent balance mutation by `(transaction_id, source_step)`.

### Public HTTP

- `GET /wallets/{user_id}/balance`
- `GET /health`

### Consumed Commands

- `wallet.debit.requested`
- `wallet.credit.requested`

### Published Outcomes

- `wallet.debited`
- `wallet.debit.rejected`
- `wallet.credited`

### Important Rules

- wallet mutation is serialized with row-level locking
- duplicate command replay must emit the original business result
- balance-after values come from the persisted wallet movement, not from later
  wallet state

## Catalog-Access

### Responsibility

- Own users, offerings, and access records.
- Decide whether a purchase is allowed.
- Decide whether a refund is eligible.
- Grant or revoke access under database constraints.

### Public HTTP

- `GET /users/{user_id}/entitlements`
- `GET /health`

### Internal HTTP

- `POST /internal/purchase-precheck`
- `POST /internal/refund-precheck`

### Consumed Commands

- `access.grant.requested`
- `access.revoke.requested`

### Published Outcomes

- `access.granted`
- `access.grant.conflicted`
- `access.revoked`
- `access.revoke.rejected`

### Important Rules

- only one active access per `(user_id, offering_id)`
- only one grant per `transaction_id`
- revocation is anchored to the original purchase transaction

## Traefik

### Responsibility

- Accept external HTTP traffic.
- Route commands to the orchestrator.
- Route reads to the owning service.

It is deliberately thin. It is not a business service and it does not contain
workflow logic.

## RabbitMQ

### Responsibility

- Carry asynchronous workflow commands.
- Carry workflow outcome events back to the orchestrator.
- Provide dead-letter routing for failed messages.

## PostgreSQL

### Responsibility

- Store each service's durable state in its own schema.
- Support transactionality for service-local writes.
- Support uniqueness constraints that enforce idempotency and domain rules.
