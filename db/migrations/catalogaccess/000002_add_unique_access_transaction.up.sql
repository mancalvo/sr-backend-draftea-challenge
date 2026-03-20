BEGIN;

CREATE UNIQUE INDEX idx_access_records_unique_transaction
    ON catalog_access.access_records (transaction_id);

COMMIT;
