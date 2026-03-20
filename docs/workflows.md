# Workflows

## Supported Functionality

The system supports:

- deposit funds into a wallet through a mocked provider
- purchase an offering with wallet balance
- refund a previous purchase by revoking access and crediting the wallet
- query transaction history, wallet balance, and active entitlements

## Purchase

```mermaid
sequenceDiagram
    actor User as End User
    participant Traefik
    participant Saga as Saga Orchestrator
    participant Catalog as Catalog-Access
    participant Payments
    participant Broker as RabbitMQ
    participant Wallets

    User->>Traefik: POST /purchases
    Traefik->>Saga: Purchase command
    Saga->>Saga: Validate + reserve idempotency key
    Saga->>Catalog: PurchasePrecheck(user_id, offering_id)
    Catalog-->>Saga: allowed + authoritative price/currency
    Saga->>Payments: Register pending purchase transaction
    Saga->>Saga: Create running saga(step = purchase_debit)
    Saga->>Broker: wallet.debit.requested
    Saga-->>Traefik: 202 Accepted(transaction_id)
    Broker->>Wallets: wallet.debit.requested
    Wallets-->>Broker: wallet.debited or wallet.debit.rejected
    Broker->>Saga: wallet outcome
    Saga->>Broker: access.grant.requested or fail transaction
    Broker->>Catalog: access.grant.requested
    Catalog-->>Broker: access.granted or access.grant.conflicted
    Broker->>Saga: access outcome
    Saga->>Payments: complete or compensate
```

### Important Behavior

- price and currency come from `catalog-access`, not from the client
- the purchase transaction is registered before the saga starts
- insufficient funds fail the transaction without compensation
- access conflict after a successful debit triggers wallet compensation

## Deposit

```mermaid
sequenceDiagram
    actor User as End User
    participant Traefik
    participant Saga as Saga Orchestrator
    participant Payments
    participant Provider as Mock Payment Provider
    participant Broker as RabbitMQ
    participant Wallets

    User->>Traefik: POST /deposits
    Traefik->>Saga: Deposit command
    Saga->>Saga: Validate + reserve idempotency key
    Saga->>Payments: Register pending deposit transaction
    Saga->>Saga: Create running saga(step = deposit_charge)
    Saga->>Broker: payments.deposit.requested
    Saga-->>Traefik: 202 Accepted(transaction_id)
    Broker->>Payments: payments.deposit.requested
    Payments->>Provider: Charge deposit
    Provider-->>Payments: Success or failure
    Payments-->>Broker: provider.charge.succeeded or provider.charge.failed
    Broker->>Saga: provider outcome
    Saga->>Broker: wallet.credit.requested or fail transaction
    Broker->>Wallets: wallet.credit.requested
    Wallets-->>Broker: wallet.credited
    Broker->>Saga: wallet.credited
    Saga->>Payments: Update final transaction status
```

### Important Behavior

- `payments` owns provider invocation and provider-result publication
- deposit processing is idempotent by `transaction_id`
- provider reference is persisted in the transaction ledger
- the timeout poller can move a deposit to `timed_out`
- a late provider outcome can still legally resolve a timed-out deposit

## Refund

```mermaid
sequenceDiagram
    actor User as End User
    participant Traefik
    participant Saga as Saga Orchestrator
    participant Catalog as Catalog-Access
    participant Payments
    participant Broker as RabbitMQ
    participant Wallets

    User->>Traefik: POST /refunds
    Traefik->>Saga: Refund command
    Saga->>Saga: Validate + reserve idempotency key
    Saga->>Catalog: RefundPrecheck(user, offering, original_tx)
    Saga->>Payments: Get original purchase transaction
    Saga->>Payments: Register pending refund transaction
    Saga->>Saga: Create running saga(step = refund_revoke_access)
    Saga->>Broker: access.revoke.requested(refund_tx, original_tx)
    Saga-->>Traefik: 202 Accepted(transaction_id)
    Broker->>Catalog: access.revoke.requested
    Catalog-->>Broker: access.revoked or access.revoke.rejected
    Broker->>Saga: revoke outcome
    Saga->>Broker: wallet.credit.requested or fail refund
    Broker->>Wallets: wallet.credit.requested
    Wallets-->>Broker: wallet.credited
    Broker->>Saga: wallet.credited
    Saga->>Payments: Update refund transaction status
```

### Important Behavior

- the client provides the original purchase transaction ID
- the refund amount and currency come from the original purchase transaction in
  `payments`
- the revoke command carries:
  - the refund transaction ID
  - the original purchase transaction ID
- only a successful revoke can trigger the wallet credit

## Read Paths

The orchestrator does not serve read models.

Reads go directly to the owning service:

- `payments` for transaction detail and transaction history
- `wallets` for current balance
- `catalog-access` for active entitlements

## Challenge Scenarios Covered

### Happy Path

- successful deposit
- successful purchase
- successful refund

### Insufficient Funds

- wallet debit is rejected
- purchase ends as `failed`
- no access is granted

### Provider Timeout

- timeout poller marks the saga and transaction as `timed_out`
- a late provider result can still complete or fail the deposit if the state
  transition is still legal

### Concurrent Payments

- ingress idempotency blocks duplicate client commands for the same key
- wallet row locking serializes concurrent wallet mutations
- access uniqueness prevents two active grants for the same user and offering
- stale or duplicated outcome events are ignored by saga step gating
