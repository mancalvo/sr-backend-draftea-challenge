-- 000002_add_idempotency_scope_and_hash.down.sql

BEGIN;

-- Restore original primary key.
ALTER TABLE saga_orchestrator.idempotency_keys
    DROP CONSTRAINT idempotency_keys_pkey;

ALTER TABLE saga_orchestrator.idempotency_keys
    ADD PRIMARY KEY (key);

-- Drop the new columns.
ALTER TABLE saga_orchestrator.idempotency_keys
    DROP COLUMN scope,
    DROP COLUMN request_hash;

COMMIT;
