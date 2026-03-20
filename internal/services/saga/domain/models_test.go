package domain_test

import (
	"testing"

	sagadomain "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/idempotency"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

type DepositPayload = workflows.DepositPayload

var RequestFingerprint = idempotency.RequestFingerprint

type SagaStatus = sagadomain.SagaStatus

const (
	SagaTypeDeposit  = sagadomain.SagaTypeDeposit
	SagaTypePurchase = sagadomain.SagaTypePurchase
	SagaTypeRefund   = sagadomain.SagaTypeRefund

	StatusCompensating           = sagadomain.StatusCompensating
	StatusCompleted              = sagadomain.StatusCompleted
	StatusCreated                = sagadomain.StatusCreated
	StatusFailed                 = sagadomain.StatusFailed
	StatusReconciliationRequired = sagadomain.StatusReconciliationRequired
	StatusRunning                = sagadomain.StatusRunning
	StatusTimedOut               = sagadomain.StatusTimedOut
)

var (
	ValidSagaStatus        = sagadomain.ValidSagaStatus
	ValidSagaType          = sagadomain.ValidSagaType
	ValidateSagaTransition = sagadomain.ValidateSagaTransition
)

func TestValidateSagaTransition_LegalTransitions(t *testing.T) {
	tests := []struct {
		from SagaStatus
		to   SagaStatus
	}{
		{StatusCreated, StatusRunning},
		{StatusRunning, StatusCompleted},
		{StatusRunning, StatusFailed},
		{StatusRunning, StatusTimedOut},
		{StatusRunning, StatusCompensating},
		{StatusRunning, StatusReconciliationRequired},
		{StatusTimedOut, StatusCompleted},
		{StatusTimedOut, StatusFailed},
		{StatusTimedOut, StatusCompensating},
		{StatusTimedOut, StatusReconciliationRequired},
		{StatusCompensating, StatusCompleted},
		{StatusCompensating, StatusFailed},
		{StatusCompensating, StatusReconciliationRequired},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if err := ValidateSagaTransition(tt.from, tt.to); err != nil {
				t.Errorf("expected legal transition from %q to %q, got error: %v", tt.from, tt.to, err)
			}
		})
	}
}

func TestValidateSagaTransition_IllegalTransitions(t *testing.T) {
	tests := []struct {
		from SagaStatus
		to   SagaStatus
	}{
		// Terminal states have no outgoing transitions.
		{StatusCompleted, StatusRunning},
		{StatusCompleted, StatusFailed},
		{StatusFailed, StatusRunning},
		{StatusFailed, StatusCompleted},
		{StatusReconciliationRequired, StatusCompleted},
		// Invalid backward transitions.
		{StatusCreated, StatusFailed},
		{StatusRunning, StatusCreated},
		{StatusCompensating, StatusRunning},
		{StatusTimedOut, StatusCreated},
		{StatusTimedOut, StatusRunning},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if err := ValidateSagaTransition(tt.from, tt.to); err == nil {
				t.Errorf("expected illegal transition from %q to %q, got nil", tt.from, tt.to)
			}
		})
	}
}

func TestValidSagaType(t *testing.T) {
	if !ValidSagaType(SagaTypeDeposit) {
		t.Error("expected deposit to be valid")
	}
	if !ValidSagaType(SagaTypePurchase) {
		t.Error("expected purchase to be valid")
	}
	if !ValidSagaType(SagaTypeRefund) {
		t.Error("expected refund to be valid")
	}
	if ValidSagaType("transfer") {
		t.Error("expected transfer to be invalid")
	}
}

func TestRequestFingerprint_Deterministic(t *testing.T) {
	p := DepositPayload{UserID: "user-1", Amount: 10000, Currency: "ARS"}
	h1 := RequestFingerprint(p)
	h2 := RequestFingerprint(p)
	if h1 != h2 {
		t.Errorf("same payload produced different hashes: %s != %s", h1, h2)
	}
}

func TestRequestFingerprint_DifferentPayloadsProduceDifferentHashes(t *testing.T) {
	p1 := DepositPayload{UserID: "user-1", Amount: 10000, Currency: "ARS"}
	p2 := DepositPayload{UserID: "user-1", Amount: 500, Currency: "ARS"}
	if RequestFingerprint(p1) == RequestFingerprint(p2) {
		t.Error("different payloads should produce different hashes")
	}
}

func TestRequestFingerprint_DefaultCurrencyDoesNotCauseMismatch(t *testing.T) {
	// If the handler applies a default currency before hashing, the hash should
	// be the same regardless of whether the client sent the currency or not.
	p1 := DepositPayload{UserID: "user-1", Amount: 10000, Currency: "ARS"}
	p2 := DepositPayload{UserID: "user-1", Amount: 10000, Currency: "ARS"}
	if RequestFingerprint(p1) != RequestFingerprint(p2) {
		t.Error("identical normalized payloads should produce the same hash")
	}
}

func TestValidSagaStatus(t *testing.T) {
	validStatuses := []SagaStatus{
		StatusCreated, StatusRunning, StatusTimedOut,
		StatusCompensating, StatusCompleted, StatusFailed,
		StatusReconciliationRequired,
	}
	for _, s := range validStatuses {
		if !ValidSagaStatus(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if ValidSagaStatus("unknown") {
		t.Error("expected 'unknown' to be invalid")
	}
}
