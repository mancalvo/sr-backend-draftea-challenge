# Messaging

## Envelope

All RabbitMQ messages use the same envelope:

- `message_id`
- `correlation_id`
- `type`
- `timestamp`
- `payload`

This keeps publish and consume behavior consistent across services and makes
logs easy to correlate.

## Exchanges

| Exchange | Type | Purpose |
| --- | --- | --- |
| `workflow.commands` | `topic` | Commands sent to domain services |
| `workflow.outcomes` | `topic` | Outcome events sent back to the orchestrator |
| `workflow.dlx` | `fanout` | Dead-letter exchange |

## Queues

| Queue | Bound Exchange | Routing Keys |
| --- | --- | --- |
| `payments.commands` | `workflow.commands` | `payments.deposit.requested` |
| `wallets.commands` | `workflow.commands` | `wallet.debit.requested`, `wallet.credit.requested` |
| `catalog_access.commands` | `workflow.commands` | `access.grant.requested`, `access.revoke.requested` |
| `saga.outcomes` | `workflow.outcomes` | all wallet, access, and provider outcomes |
| `workflow.dlq` | `workflow.dlx` | dead-letter catch-all |

## Command Catalog

### `payments.deposit.requested`

Owner:
- published by `saga-orchestrator`
- consumed by `payments`

Purpose:
- start provider-side deposit processing

### `wallet.debit.requested`

Owner:
- published by `saga-orchestrator`
- consumed by `wallets`

Purpose:
- debit wallet funds for a purchase

### `wallet.credit.requested`

Owner:
- published by `saga-orchestrator`
- consumed by `wallets`

Purpose:
- credit wallet funds for:
  - deposit completion
  - purchase compensation
  - refund completion

### `access.grant.requested`

Owner:
- published by `saga-orchestrator`
- consumed by `catalog-access`

Purpose:
- grant access for a successful purchase debit

### `access.revoke.requested`

Owner:
- published by `saga-orchestrator`
- consumed by `catalog-access`

Purpose:
- revoke access for a refund

Important payload detail:
- the message carries the refund transaction ID and the original purchase
  transaction ID

## Outcome Catalog

### Wallet Outcomes

- `wallet.debited`
- `wallet.debit.rejected`
- `wallet.credited`

### Access Outcomes

- `access.granted`
- `access.grant.conflicted`
- `access.revoked`
- `access.revoke.rejected`

### Provider Outcomes

- `provider.charge.succeeded`
- `provider.charge.failed`

## Delivery Semantics

The implementation assumes at-least-once delivery.

Because of that:

- command ingress uses persisted idempotency keys
- wallet mutation is idempotent by `(transaction_id, source_step)`
- access grant is idempotent by `transaction_id`
- deposit processing is idempotent by `transaction_id`
- saga outcome handling is gated by `current_step`

The system does not claim exactly-once messaging.

## Why Some Steps Stay Synchronous

Not every interaction is forced through RabbitMQ.

The orchestrator still calls `payments` and `catalog-access` synchronously when
it needs:

- an immediate authoritative transaction registration
- an immediate authoritative ledger update
- an immediate eligibility decision before starting a workflow

That keeps ownership clear and avoids turning simple request-response decisions
into artificial asynchronous handshakes.

## Dead Letters And Retries

RabbitMQ queues are configured with a dead-letter exchange.

The shared consumer runtime implements bounded retries for handler failures.
This gives the system a challenge-scope safety net without building a large
messaging framework.
