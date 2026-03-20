BEGIN;

DROP INDEX IF EXISTS payments.idx_transactions_original_transaction_id;

ALTER TABLE payments.transactions
    DROP COLUMN IF EXISTS provider_reference,
    DROP COLUMN IF EXISTS original_transaction_id;

COMMIT;
