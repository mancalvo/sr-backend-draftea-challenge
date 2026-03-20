package domain

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Pagination defaults and limits.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 100
)

// ErrInvalidCursor is returned when a cursor string cannot be decoded.
var ErrInvalidCursor = errors.New("invalid cursor")

// ListTransactionsQuery holds the parameters for a paginated transaction list.
type ListTransactionsQuery struct {
	UserID string
	Limit  int
	Cursor *Cursor
}

// Cursor encodes the position of the last item on the previous page.
// It contains enough information to continue the (created_at DESC, id DESC) ordering.
type Cursor struct {
	CreatedAt time.Time `json:"ca"`
	ID        string    `json:"id"`
}

// EncodeCursor serialises a cursor into a URL-safe, opaque string.
func EncodeCursor(c Cursor) string {
	b, _ := json.Marshal(c)
	return base64.URLEncoding.EncodeToString(b)
}

// DecodeCursor deserialises a cursor string produced by EncodeCursor.
// Returns ErrInvalidCursor if the string is malformed.
func DecodeCursor(s string) (*Cursor, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, ErrInvalidCursor
	}
	if c.ID == "" || c.CreatedAt.IsZero() {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}

// ListTransactionsResult is the paginated result returned by the repository.
type ListTransactionsResult struct {
	Transactions []Transaction
	NextCursor   *string
	HasMore      bool
}

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
	ID                    string            `json:"id"`
	UserID                string            `json:"user_id"`
	Type                  TransactionType   `json:"type"`
	Status                TransactionStatus `json:"status"`
	Amount                int64             `json:"amount"`
	Currency              string            `json:"currency"`
	OfferingID            *string           `json:"offering_id,omitempty"`
	OriginalTransactionID *string           `json:"original_transaction_id,omitempty"`
	ProviderReference     *string           `json:"provider_reference,omitempty"`
	StatusReason          *string           `json:"status_reason,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
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
