-- 000002_create_wallets_schema.up.sql
-- Schema: wallets
-- Owns: wallets, wallet_movements
-- Correctness: row-level locking, atomic balance mutation, movement dedupe

BEGIN;

CREATE SCHEMA IF NOT EXISTS wallets;

CREATE TABLE wallets.wallets (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL,
    balance     BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    currency    TEXT        NOT NULL DEFAULT 'ARS',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Unique wallet per user in v1
CREATE UNIQUE INDEX idx_wallets_user_id
    ON wallets.wallets (user_id);

CREATE TYPE wallets.movement_type AS ENUM ('credit', 'debit');

CREATE TABLE wallets.wallet_movements (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id       UUID        NOT NULL REFERENCES wallets.wallets (id),
    transaction_id  UUID        NOT NULL,
    source_step     TEXT        NOT NULL,
    type            wallets.movement_type NOT NULL,
    amount          BIGINT      NOT NULL CHECK (amount > 0),
    balance_before  BIGINT      NOT NULL,
    balance_after   BIGINT      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Dedupe by business effect: (transaction_id, source_step)
-- Ensures idempotent wallet operations per saga step
CREATE UNIQUE INDEX idx_wallet_movements_dedupe
    ON wallets.wallet_movements (transaction_id, source_step);

-- Index for wallet movement history lookups
CREATE INDEX idx_wallet_movements_wallet_id
    ON wallets.wallet_movements (wallet_id, created_at DESC);

COMMIT;
