BEGIN;

ALTER TABLE saga_orchestrator.idempotency_keys
    ADD COLUMN state TEXT NOT NULL DEFAULT 'completed',
    ADD CONSTRAINT idempotency_keys_state_check
        CHECK (state IN ('processing', 'completed'));

COMMIT;
