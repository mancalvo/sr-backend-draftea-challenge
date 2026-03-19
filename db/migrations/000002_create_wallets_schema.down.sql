-- 000002_create_wallets_schema.down.sql

BEGIN;

DROP TABLE IF EXISTS wallets.wallet_movements;
DROP TABLE IF EXISTS wallets.wallets;
DROP TYPE IF EXISTS wallets.movement_type;
DROP SCHEMA IF EXISTS wallets;

COMMIT;
