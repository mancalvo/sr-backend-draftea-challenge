package domain

import (
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

var legalSagaTransitions = map[SagaStatus]map[SagaStatus]bool{
	StatusCreated: {
		StatusRunning: true,
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

// IdempotencyState represents the lifecycle state of a stored idempotent command.
type IdempotencyState string

const (
	IdempotencyStateProcessing IdempotencyState = "processing"
	IdempotencyStateCompleted  IdempotencyState = "completed"
)

// IdempotencyKey stores the result of a previously accepted command.
type IdempotencyKey struct {
	Key            string           `json:"key"`
	Scope          string           `json:"scope"`
	RequestHash    string           `json:"request_hash"`
	TransactionID  string           `json:"transaction_id"`
	State          IdempotencyState `json:"state"`
	ResponseStatus int              `json:"response_status"`
	ResponseBody   json.RawMessage  `json:"response_body,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	ExpiresAt      time.Time        `json:"expires_at"`
}
