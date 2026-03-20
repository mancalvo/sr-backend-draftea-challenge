BEGIN;

ALTER TABLE saga_orchestrator.idempotency_keys
    DROP CONSTRAINT IF EXISTS idempotency_keys_state_check,
    DROP COLUMN IF EXISTS state;

COMMIT;
