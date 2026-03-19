-- 000003_create_catalog_access_schema.down.sql

BEGIN;

DROP TABLE IF EXISTS catalog_access.access_records;
DROP TABLE IF EXISTS catalog_access.offerings;
DROP TABLE IF EXISTS catalog_access.users;
DROP TYPE IF EXISTS catalog_access.access_status;
DROP SCHEMA IF EXISTS catalog_access;

COMMIT;
