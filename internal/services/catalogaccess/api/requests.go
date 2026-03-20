package api

// PurchasePrecheckRequest is the body for POST /internal/purchase-precheck.
type PurchasePrecheckRequest struct {
	UserID     string `json:"user_id"`
	OfferingID string `json:"offering_id"`
}

// RefundPrecheckRequest is the body for POST /internal/refund-precheck.
type RefundPrecheckRequest struct {
	UserID        string `json:"user_id"`
	OfferingID    string `json:"offering_id"`
	TransactionID string `json:"transaction_id"`
}
