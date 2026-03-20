-- Sample local bootstrap data for manual testing.
-- Run this after the services have created their schemas and migrations.
--
-- Example:
-- psql "postgres://draftea:draftea@localhost:5432/draftea?sslmode=disable" \
--   -f docs/examples/bootstrap_sample_data.sql

BEGIN;

INSERT INTO catalog_access.users (id, email, name)
VALUES (
    '11111111-1111-1111-1111-111111111111',
    'user@example.com',
    'Sample User'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO wallets.wallets (id, user_id, balance, currency)
VALUES (
    '22222222-2222-2222-2222-222222222222',
    '11111111-1111-1111-1111-111111111111',
    250000,
    'ARS'
)
ON CONFLICT (user_id) DO NOTHING;

INSERT INTO catalog_access.offerings (id, name, description, price, currency, active)
VALUES (
    '33333333-3333-3333-3333-333333333333',
    'Premium Course',
    'Sample offering for purchase and refund testing',
    50000,
    'ARS',
    true
)
ON CONFLICT (id) DO NOTHING;

COMMIT;
