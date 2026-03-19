// Package payments implements the payments service: transaction ownership,
// transaction state machine, transaction queries, and provider abstraction.
package payments

import (
	"fmt"
	"time"
)

// TransactionType represents the kind of transaction.
type TransactionType string

const (
	TransactionTypeDeposit  TransactionType = "deposit"
	TransactionTypePurchase TransactionType = "purchase"
	TransactionTypeRefund   TransactionType = "refund"
)

// TransactionStatus represents the lifecycle state of a transaction.
type TransactionStatus string

const (
	StatusPending                TransactionStatus = "pending"
	StatusCompleted              TransactionStatus = "completed"
	StatusFailed                 TransactionStatus = "failed"
	StatusTimedOut               TransactionStatus = "timed_out"
	StatusCompensated            TransactionStatus = "compensated"
	StatusReconciliationRequired TransactionStatus = "reconciliation_required"
)

// Transaction represents a transaction record owned by the payments service.
type Transaction struct {
	ID           string            `json:"id"`
	UserID       string            `json:"user_id"`
	Type         TransactionType   `json:"type"`
	Status       TransactionStatus `json:"status"`
	Amount       int64             `json:"amount"`
	Currency     string            `json:"currency"`
	OfferingID   *string           `json:"offering_id,omitempty"`
	StatusReason *string           `json:"status_reason,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// CreateTransactionRequest is the body for POST /internal/transactions.
type CreateTransactionRequest struct {
	ID         string          `json:"id"`
	UserID     string          `json:"user_id"`
	Type       TransactionType `json:"type"`
	Amount     int64           `json:"amount"`
	Currency   string          `json:"currency"`
	OfferingID *string         `json:"offering_id,omitempty"`
}

// UpdateStatusRequest is the body for PATCH /internal/transactions/{transaction_id}/status.
type UpdateStatusRequest struct {
	Status       TransactionStatus `json:"status"`
	StatusReason *string           `json:"status_reason,omitempty"`
}

// legalTransitions defines the allowed state transitions for the transaction state machine.
// Key: current status -> Value: set of allowed target statuses.
var legalTransitions = map[TransactionStatus]map[TransactionStatus]bool{
	StatusPending: {
		StatusCompleted:              true,
		StatusFailed:                 true,
		StatusTimedOut:               true,
		StatusCompensated:            true,
		StatusReconciliationRequired: true,
	},
	StatusTimedOut: {
		StatusCompleted:              true,
		StatusFailed:                 true,
		StatusReconciliationRequired: true,
	},
}

// ValidateTransition checks whether moving from `from` to `to` is a legal state transition.
// Returns an error if the transition is not allowed.
func ValidateTransition(from, to TransactionStatus) error {
	targets, ok := legalTransitions[from]
	if !ok {
		return fmt.Errorf("no transitions allowed from status %q", from)
	}
	if !targets[to] {
		return fmt.Errorf("transition from %q to %q is not allowed", from, to)
	}
	return nil
}

// ValidTransactionType checks whether a string is a valid transaction type.
func ValidTransactionType(t TransactionType) bool {
	switch t {
	case TransactionTypeDeposit, TransactionTypePurchase, TransactionTypeRefund:
		return true
	}
	return false
}

// ValidTransactionStatus checks whether a string is a valid transaction status.
func ValidTransactionStatus(s TransactionStatus) bool {
	switch s {
	case StatusPending, StatusCompleted, StatusFailed, StatusTimedOut,
		StatusCompensated, StatusReconciliationRequired:
		return true
	}
	return false
}
