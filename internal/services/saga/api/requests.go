package api

// DepositCommand is the request body for POST /deposits.
type DepositCommand struct {
	UserID         string `json:"user_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	IdempotencyKey string `json:"idempotency_key"`
}

// PurchaseCommand is the request body for POST /purchases.
type PurchaseCommand struct {
	UserID         string `json:"user_id"`
	OfferingID     string `json:"offering_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// RefundCommand is the request body for POST /refunds.
type RefundCommand struct {
	UserID         string `json:"user_id"`
	OfferingID     string `json:"offering_id"`
	TransactionID  string `json:"transaction_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// CommandAcceptedResponse is the response body for a successfully accepted command.
type CommandAcceptedResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}
