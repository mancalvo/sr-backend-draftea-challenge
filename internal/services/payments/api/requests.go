package api

import "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"

// CreateTransactionRequest is the body for POST /internal/transactions.
type CreateTransactionRequest struct {
	ID                    string                 `json:"id"`
	UserID                string                 `json:"user_id"`
	Type                  domain.TransactionType `json:"type"`
	Amount                int64                  `json:"amount"`
	Currency              string                 `json:"currency"`
	OfferingID            *string                `json:"offering_id,omitempty"`
	OriginalTransactionID *string                `json:"original_transaction_id,omitempty"`
}

// UpdateStatusRequest is the body for PATCH /internal/transactions/{transaction_id}/status.
type UpdateStatusRequest struct {
	Status            domain.TransactionStatus `json:"status"`
	StatusReason      *string                  `json:"status_reason,omitempty"`
	ProviderReference *string                  `json:"provider_reference,omitempty"`
}
