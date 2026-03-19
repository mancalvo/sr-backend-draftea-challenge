-- 000003_create_catalog_access_schema.up.sql
-- Schema: catalog_access
-- Owns: users, offerings, access_records

BEGIN;

CREATE SCHEMA IF NOT EXISTS catalog_access;

CREATE TABLE catalog_access.users (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email       TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_users_email
    ON catalog_access.users (email);

CREATE TABLE catalog_access.offerings (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    description TEXT,
    price       BIGINT      NOT NULL CHECK (price > 0),
    currency    TEXT        NOT NULL DEFAULT 'ARS',
    active      BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TYPE catalog_access.access_status AS ENUM ('active', 'revoked');

CREATE TABLE catalog_access.access_records (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES catalog_access.users (id),
    offering_id     UUID        NOT NULL REFERENCES catalog_access.offerings (id),
    transaction_id  UUID        NOT NULL,
    status          catalog_access.access_status NOT NULL DEFAULT 'active',
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Unique active access: only one active access per user+offering
CREATE UNIQUE INDEX idx_access_records_unique_active
    ON catalog_access.access_records (user_id, offering_id)
    WHERE status = 'active';

-- Index for looking up access by transaction (for revoke operations)
CREATE INDEX idx_access_records_transaction_id
    ON catalog_access.access_records (transaction_id);

-- Index for user entitlements query
CREATE INDEX idx_access_records_user_id_status
    ON catalog_access.access_records (user_id, status);

COMMIT;
