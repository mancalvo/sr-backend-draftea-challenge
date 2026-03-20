BEGIN;

ALTER TABLE payments.transactions
    ADD COLUMN original_transaction_id UUID REFERENCES payments.transactions(id),
    ADD COLUMN provider_reference TEXT;

CREATE INDEX idx_transactions_original_transaction_id
    ON payments.transactions (original_transaction_id);

COMMIT;
