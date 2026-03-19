// Package wallets implements the wallets service: wallet balances, wallet movements,
// and concurrency-safe debit/credit operations.
package wallets

import "time"

// MovementType represents the type of wallet movement.
type MovementType string

const (
	MovementTypeCredit MovementType = "credit"
	MovementTypeDebit  MovementType = "debit"
)

// Wallet represents a user's wallet with a balance.
type Wallet struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Balance   int64     `json:"balance"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WalletMovement represents a single balance mutation in the wallet journal.
type WalletMovement struct {
	ID            string       `json:"id"`
	WalletID      string       `json:"wallet_id"`
	TransactionID string       `json:"transaction_id"`
	SourceStep    string       `json:"source_step"`
	Type          MovementType `json:"type"`
	Amount        int64        `json:"amount"`
	BalanceBefore int64        `json:"balance_before"`
	BalanceAfter  int64        `json:"balance_after"`
	CreatedAt     time.Time    `json:"created_at"`
}

// BalanceResponse is the public response for GET /wallets/{user_id}/balance.
type BalanceResponse struct {
	UserID   string `json:"user_id"`
	Balance  int64  `json:"balance"`
	Currency string `json:"currency"`
}
