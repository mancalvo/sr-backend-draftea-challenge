-- 000001_create_payments_schema.down.sql

BEGIN;

DROP TABLE IF EXISTS payments.transactions;
DROP TYPE IF EXISTS payments.transaction_status;
DROP TYPE IF EXISTS payments.transaction_type;
DROP SCHEMA IF EXISTS payments;

COMMIT;
