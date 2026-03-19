-- 000001_create_payments_schema.up.sql
-- Schema: payments
-- Owns: transactions, transaction state machine

BEGIN;

CREATE SCHEMA IF NOT EXISTS payments;

-- Transaction statuses: pending, completed, failed, timed_out, compensated, reconciliation_required
CREATE TYPE payments.transaction_type AS ENUM ('deposit', 'purchase', 'refund');

CREATE TYPE payments.transaction_status AS ENUM (
    'pending',
    'completed',
    'failed',
    'timed_out',
    'compensated',
    'reconciliation_required'
);

CREATE TABLE payments.transactions (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL,
    type            payments.transaction_type   NOT NULL,
    status          payments.transaction_status NOT NULL DEFAULT 'pending',
    amount          BIGINT      NOT NULL CHECK (amount > 0),
    currency        TEXT        NOT NULL DEFAULT 'ARS',
    offering_id     UUID,
    status_reason   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for user transaction history queries (GET /transactions?user_id=)
CREATE INDEX idx_transactions_user_id_created_at
    ON payments.transactions (user_id, created_at DESC);

-- Index for status-based queries (e.g., finding timed_out transactions)
CREATE INDEX idx_transactions_status
    ON payments.transactions (status);

COMMIT;
