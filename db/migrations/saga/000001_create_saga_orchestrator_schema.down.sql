-- 000001_create_saga_orchestrator_schema.down.sql

BEGIN;

DROP TABLE IF EXISTS saga_orchestrator.idempotency_keys;
DROP TABLE IF EXISTS saga_orchestrator.saga_instances;
DROP TYPE IF EXISTS saga_orchestrator.saga_outcome;
DROP TYPE IF EXISTS saga_orchestrator.saga_status;
DROP TYPE IF EXISTS saga_orchestrator.saga_type;
DROP SCHEMA IF EXISTS saga_orchestrator;

COMMIT;
