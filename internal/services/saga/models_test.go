package saga

import "testing"

func TestValidateSagaTransition_LegalTransitions(t *testing.T) {
	tests := []struct {
		from SagaStatus
		to   SagaStatus
	}{
		{StatusCreated, StatusRunning},
		{StatusCreated, StatusFailed},
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
