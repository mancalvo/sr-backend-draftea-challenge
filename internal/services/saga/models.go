// Package saga implements the saga-orchestrator service: command ingress,
// idempotency, saga state persistence, and workflow engine behavior.
package saga

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// SagaType represents the kind of workflow.
type SagaType string

const (
	SagaTypeDeposit  SagaType = "deposit"
	SagaTypePurchase SagaType = "purchase"
	SagaTypeRefund   SagaType = "refund"
)

// ValidSagaType checks whether a string is a valid saga type.
func ValidSagaType(t SagaType) bool {
	switch t {
	case SagaTypeDeposit, SagaTypePurchase, SagaTypeRefund:
		return true
	}
	return false
}

// SagaStatus represents the lifecycle state of a saga instance.
type SagaStatus string

const (
	StatusCreated                SagaStatus = "created"
	StatusRunning                SagaStatus = "running"
	StatusTimedOut               SagaStatus = "timed_out"
	StatusCompensating           SagaStatus = "compensating"
	StatusCompleted              SagaStatus = "completed"
	StatusFailed                 SagaStatus = "failed"
	StatusReconciliationRequired SagaStatus = "reconciliation_required"
)

// ValidSagaStatus checks whether a string is a valid saga status.
func ValidSagaStatus(s SagaStatus) bool {
	switch s {
	case StatusCreated, StatusRunning, StatusTimedOut, StatusCompensating,
		StatusCompleted, StatusFailed, StatusReconciliationRequired:
		return true
	}
	return false
}

// SagaOutcome represents the final outcome of a completed saga.
type SagaOutcome string

const (
	OutcomeSucceeded   SagaOutcome = "succeeded"
	OutcomeFailed      SagaOutcome = "failed"
	OutcomeCompensated SagaOutcome = "compensated"
)

// legalSagaTransitions defines the allowed state transitions for the saga state machine.
var legalSagaTransitions = map[SagaStatus]map[SagaStatus]bool{
	StatusCreated: {
		StatusRunning: true,
		StatusFailed:  true,
	},
	StatusRunning: {
		StatusCompleted:              true,
		StatusFailed:                 true,
		StatusTimedOut:               true,
		StatusCompensating:           true,
		StatusReconciliationRequired: true,
	},
	StatusTimedOut: {
		StatusCompleted:              true,
		StatusFailed:                 true,
		StatusCompensating:           true,
		StatusReconciliationRequired: true,
	},
	StatusCompensating: {
		StatusCompleted:              true,
		StatusFailed:                 true,
		StatusReconciliationRequired: true,
	},
}

// ValidateSagaTransition checks whether moving from `from` to `to` is a legal saga state transition.
func ValidateSagaTransition(from, to SagaStatus) error {
	targets, ok := legalSagaTransitions[from]
	if !ok {
		return fmt.Errorf("no transitions allowed from saga status %q", from)
	}
	if !targets[to] {
		return fmt.Errorf("saga transition from %q to %q is not allowed", from, to)
	}
	return nil
}

// SagaInstance represents a persisted saga workflow instance.
type SagaInstance struct {
	ID            string          `json:"id"`
	TransactionID string          `json:"transaction_id"`
	Type          SagaType        `json:"type"`
	Status        SagaStatus      `json:"status"`
	Outcome       *SagaOutcome    `json:"outcome,omitempty"`
	CurrentStep   *string         `json:"current_step,omitempty"`
	Payload       json.RawMessage `json:"payload"`
	TimeoutAt     *time.Time      `json:"timeout_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// IdempotencyKey stores the result of a previously accepted command so that
// duplicate requests receive the same response. The uniqueness constraint is
// on (Scope, Key). RequestHash is used to detect payload mismatches.
type IdempotencyKey struct {
	Key            string          `json:"key"`
	Scope          string          `json:"scope"`
	RequestHash    string          `json:"request_hash"`
	TransactionID  string          `json:"transaction_id"`
	ResponseStatus int             `json:"response_status"`
	ResponseBody   json.RawMessage `json:"response_body,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
}

// RequestFingerprint computes a SHA-256 hex digest of the canonical JSON
// representation of a validated command struct. The struct is marshalled with
// Go's encoding/json (sorted keys), producing a stable fingerprint that is
// insensitive to whitespace, field ordering, or default-value differences in
// the raw request body.
func RequestFingerprint(cmd any) string {
	b, _ := json.Marshal(cmd)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ---- Command ingress request types ----

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
	TransactionID  string `json:"transaction_id"` // original purchase transaction
	IdempotencyKey string `json:"idempotency_key"`
}

// CommandAcceptedResponse is the response body for a successfully accepted command (202).
type CommandAcceptedResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

// ---- Saga payload types (stored in saga_instances.payload) ----

// DepositPayload is the JSONB payload stored in a deposit saga.
type DepositPayload struct {
	UserID   string `json:"user_id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// PurchasePayload is the JSONB payload stored in a purchase saga.
type PurchasePayload struct {
	UserID     string `json:"user_id"`
	OfferingID string `json:"offering_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
}

// RefundPayload is the JSONB payload stored in a refund saga.
type RefundPayload struct {
	UserID              string `json:"user_id"`
	OfferingID          string `json:"offering_id"`
	OriginalTransaction string `json:"original_transaction"`
	Amount              int64  `json:"amount"`
	Currency            string `json:"currency"`
}
