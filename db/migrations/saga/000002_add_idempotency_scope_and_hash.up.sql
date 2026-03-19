-- 000002_add_idempotency_scope_and_hash.up.sql
-- Adds scope and request_hash columns to idempotency_keys, and replaces the
-- primary key with a composite unique constraint on (scope, key).

BEGIN;

-- Add the new columns with defaults so existing rows are backfilled.
ALTER TABLE saga_orchestrator.idempotency_keys
    ADD COLUMN scope        TEXT NOT NULL DEFAULT 'legacy',
    ADD COLUMN request_hash TEXT NOT NULL DEFAULT '';

-- Drop the old primary key (key only).
ALTER TABLE saga_orchestrator.idempotency_keys
    DROP CONSTRAINT idempotency_keys_pkey;

-- Add new composite primary key on (scope, key).
ALTER TABLE saga_orchestrator.idempotency_keys
    ADD PRIMARY KEY (scope, key);

-- Remove the defaults now that backfill is complete.
ALTER TABLE saga_orchestrator.idempotency_keys
    ALTER COLUMN scope DROP DEFAULT,
    ALTER COLUMN request_hash DROP DEFAULT;

COMMIT;
