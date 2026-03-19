-- 000001_create_saga_orchestrator_schema.up.sql
-- Schema: saga_orchestrator
-- Owns: saga_instances, idempotency_keys

BEGIN;

CREATE SCHEMA IF NOT EXISTS saga_orchestrator;

CREATE TYPE saga_orchestrator.saga_type AS ENUM ('deposit', 'purchase', 'refund');

CREATE TYPE saga_orchestrator.saga_status AS ENUM (
    'created',
    'running',
    'timed_out',
    'compensating',
    'completed',
    'failed',
    'reconciliation_required'
);

CREATE TYPE saga_orchestrator.saga_outcome AS ENUM (
    'succeeded',
    'failed',
    'compensated'
);

CREATE TABLE saga_orchestrator.saga_instances (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  UUID        NOT NULL,
    type            saga_orchestrator.saga_type   NOT NULL,
    status          saga_orchestrator.saga_status NOT NULL DEFAULT 'created',
    outcome         saga_orchestrator.saga_outcome,
    current_step    TEXT,
    payload         JSONB       NOT NULL DEFAULT '{}',
    timeout_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One saga per transaction
CREATE UNIQUE INDEX idx_saga_instances_transaction_id
    ON saga_orchestrator.saga_instances (transaction_id);

-- Index for finding timed-out sagas (poller query)
CREATE INDEX idx_saga_instances_timeout
    ON saga_orchestrator.saga_instances (timeout_at)
    WHERE status IN ('running', 'created');

-- Index for status-based lookups
CREATE INDEX idx_saga_instances_status
    ON saga_orchestrator.saga_instances (status);

-- Idempotency at ingress: prevents duplicate command processing
CREATE TABLE saga_orchestrator.idempotency_keys (
    key             TEXT        PRIMARY KEY,
    transaction_id  UUID        NOT NULL,
    response_status INT         NOT NULL,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '24 hours'
);

-- Index for cleanup of expired keys
CREATE INDEX idx_idempotency_keys_expires_at
    ON saga_orchestrator.idempotency_keys (expires_at);

COMMIT;
