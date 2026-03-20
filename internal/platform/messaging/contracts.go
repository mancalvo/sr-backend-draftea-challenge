package messaging

// This file defines typed payload structs for all command and outcome messages
// exchanged between services through RabbitMQ.

// ---------- Command payloads (published to workflow.commands) ----------

// DepositRequested is the payload for routing key "payments.deposit.requested".
// Instructs the payments service to execute a deposit via the external provider.
type DepositRequested struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
}

// WalletDebitRequested is the payload for routing key "wallet.debit.requested".
// Instructs the wallets service to debit a user's wallet.
type WalletDebitRequested struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	SourceStep    string `json:"source_step"`
}

// WalletCreditRequested is the payload for routing key "wallet.credit.requested".
// Instructs the wallets service to credit a user's wallet.
type WalletCreditRequested struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	SourceStep    string `json:"source_step"`
}

// AccessGrantRequested is the payload for routing key "access.grant.requested".
// Instructs the catalog-access service to grant access to an offering.
type AccessGrantRequested struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	OfferingID    string `json:"offering_id"`
}

// AccessRevokeRequested is the payload for routing key "access.revoke.requested".
// Instructs the catalog-access service to revoke access to an offering.
type AccessRevokeRequested struct {
	TransactionID         string `json:"transaction_id"`
	OriginalTransactionID string `json:"original_transaction_id"`
	UserID                string `json:"user_id"`
	OfferingID            string `json:"offering_id"`
}

// ---------- Outcome payloads (published to workflow.outcomes) ----------

// WalletDebited is the payload for routing key "wallet.debited".
// Indicates a wallet debit completed successfully.
type WalletDebited struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	BalanceAfter  int64  `json:"balance_after"`
	SourceStep    string `json:"source_step"`
}

// WalletDebitRejected is the payload for routing key "wallet.debit.rejected".
// Indicates a wallet debit was rejected (e.g. insufficient funds).
type WalletDebitRejected struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	Reason        string `json:"reason"`
	SourceStep    string `json:"source_step"`
}

// WalletCredited is the payload for routing key "wallet.credited".
// Indicates a wallet credit completed successfully.
type WalletCredited struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	BalanceAfter  int64  `json:"balance_after"`
	SourceStep    string `json:"source_step"`
}

// AccessGranted is the payload for routing key "access.granted".
// Indicates access was granted to the user for the offering.
type AccessGranted struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	OfferingID    string `json:"offering_id"`
}

// AccessGrantConflicted is the payload for routing key "access.grant.conflicted".
// Indicates the user already has active access to the offering.
type AccessGrantConflicted struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	OfferingID    string `json:"offering_id"`
	Reason        string `json:"reason"`
}

// AccessRevoked is the payload for routing key "access.revoked".
// Indicates access was revoked for the user from the offering.
type AccessRevoked struct {
	TransactionID         string `json:"transaction_id"`
	OriginalTransactionID string `json:"original_transaction_id,omitempty"`
	UserID                string `json:"user_id"`
	OfferingID            string `json:"offering_id"`
}

// AccessRevokeRejected is the payload for routing key "access.revoke.rejected".
// Indicates the access revocation was rejected (e.g. no active access found).
type AccessRevokeRejected struct {
	TransactionID         string `json:"transaction_id"`
	OriginalTransactionID string `json:"original_transaction_id,omitempty"`
	UserID                string `json:"user_id"`
	OfferingID            string `json:"offering_id"`
	Reason                string `json:"reason"`
}

// ProviderChargeSucceeded is the payload for routing key "provider.charge.succeeded".
// Indicates the external provider charge completed successfully.
type ProviderChargeSucceeded struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	ProviderRef   string `json:"provider_ref"`
}

// ProviderChargeFailed is the payload for routing key "provider.charge.failed".
// Indicates the external provider charge failed.
type ProviderChargeFailed struct {
	TransactionID string `json:"transaction_id"`
	UserID        string `json:"user_id"`
	Amount        int64  `json:"amount"`
	Reason        string `json:"reason"`
}
