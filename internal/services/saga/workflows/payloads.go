package workflows

import "github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"

// DepositPayload is the JSON payload stored in a deposit saga.
type DepositPayload struct {
	UserID       string                          `json:"user_id"`
	Amount       int64                           `json:"amount"`
	Currency     string                          `json:"currency"`
	MockProvider *messaging.MockProviderControls `json:"mock_provider,omitempty"`
}

// PurchasePayload is the JSON payload stored in a purchase saga.
type PurchasePayload struct {
	UserID     string `json:"user_id"`
	OfferingID string `json:"offering_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
}

// RefundPayload is the JSON payload stored in a refund saga.
type RefundPayload struct {
	UserID              string `json:"user_id"`
	OfferingID          string `json:"offering_id"`
	OriginalTransaction string `json:"original_transaction"`
	Amount              int64  `json:"amount"`
	Currency            string `json:"currency"`
}
