package payments

import "testing"

func TestValidateTransition_AllLegalPaths(t *testing.T) {
	legal := []struct {
		from TransactionStatus
		to   TransactionStatus
	}{
		{StatusPending, StatusCompleted},
		{StatusPending, StatusFailed},
		{StatusPending, StatusTimedOut},
		{StatusPending, StatusCompensated},
		{StatusPending, StatusReconciliationRequired},
		{StatusTimedOut, StatusCompleted},
		{StatusTimedOut, StatusFailed},
		{StatusTimedOut, StatusReconciliationRequired},
	}

	for _, tt := range legal {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if err := ValidateTransition(tt.from, tt.to); err != nil {
				t.Errorf("expected legal, got error: %v", err)
			}
		})
	}
}

func TestValidateTransition_IllegalPaths(t *testing.T) {
	illegal := []struct {
		from TransactionStatus
		to   TransactionStatus
	}{
		{StatusCompleted, StatusPending},
		{StatusCompleted, StatusFailed},
		{StatusCompleted, StatusTimedOut},
		{StatusFailed, StatusPending},
		{StatusFailed, StatusCompleted},
		{StatusCompensated, StatusPending},
		{StatusCompensated, StatusCompleted},
		{StatusReconciliationRequired, StatusPending},
		{StatusReconciliationRequired, StatusCompleted},
		{StatusTimedOut, StatusPending},
		{StatusTimedOut, StatusCompensated},
	}

	for _, tt := range illegal {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if err := ValidateTransition(tt.from, tt.to); err == nil {
				t.Errorf("expected illegal, got nil")
			}
		})
	}
}

func TestValidTransactionType(t *testing.T) {
	tests := []struct {
		input TransactionType
		want  bool
	}{
		{TransactionTypeDeposit, true},
		{TransactionTypePurchase, true},
		{TransactionTypeRefund, true},
		{TransactionType("transfer"), false},
		{TransactionType(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := ValidTransactionType(tt.input)
			if got != tt.want {
				t.Errorf("ValidTransactionType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidTransactionStatus(t *testing.T) {
	tests := []struct {
		input TransactionStatus
		want  bool
	}{
		{StatusPending, true},
		{StatusCompleted, true},
		{StatusFailed, true},
		{StatusTimedOut, true},
		{StatusCompensated, true},
		{StatusReconciliationRequired, true},
		{TransactionStatus("unknown"), false},
		{TransactionStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := ValidTransactionStatus(tt.input)
			if got != tt.want {
				t.Errorf("ValidTransactionStatus(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
