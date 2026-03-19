package payments

import (
	"testing"
	"time"
)

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

// --- Cursor encoding/decoding ---

func TestEncodeDecode_Cursor_RoundTrip(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	original := Cursor{CreatedAt: ts, ID: "txn-42"}

	encoded := EncodeCursor(original)
	if encoded == "" {
		t.Fatal("encoded cursor should not be empty")
	}

	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decoded.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("created_at = %v, want %v", decoded.CreatedAt, original.CreatedAt)
	}
	if decoded.ID != original.ID {
		t.Errorf("id = %v, want %v", decoded.ID, original.ID)
	}
}

func TestDecodeCursor_InvalidBase64(t *testing.T) {
	_, err := DecodeCursor("not-valid-base64!@#$")
	if err != ErrInvalidCursor {
		t.Errorf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestDecodeCursor_InvalidJSON(t *testing.T) {
	// Valid base64 but not valid JSON.
	_, err := DecodeCursor("bm90LWpzb24=")
	if err != ErrInvalidCursor {
		t.Errorf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestDecodeCursor_MissingFields(t *testing.T) {
	// Valid JSON but missing required fields (empty ID, zero time).
	encoded := EncodeCursor(Cursor{})
	_, err := DecodeCursor(encoded)
	if err != ErrInvalidCursor {
		t.Errorf("expected ErrInvalidCursor for empty cursor, got %v", err)
	}
}
