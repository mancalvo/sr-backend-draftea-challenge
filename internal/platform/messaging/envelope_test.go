package messaging

import (
	"encoding/json"
	"testing"
)

func TestNewEnvelope_SetsFields(t *testing.T) {
	payload := map[string]string{"transaction_id": "txn-123"}
	env, err := NewEnvelope("wallet.debit.requested", "corr-456", payload)
	if err != nil {
		t.Fatalf("NewEnvelope returned error: %v", err)
	}

	if env.MessageID == "" {
		t.Error("MessageID should not be empty")
	}
	if env.CorrelationID != "corr-456" {
		t.Errorf("CorrelationID = %q, want %q", env.CorrelationID, "corr-456")
	}
	if env.Type != "wallet.debit.requested" {
		t.Errorf("Type = %q, want %q", env.Type, "wallet.debit.requested")
	}
	if env.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	if env.Payload == nil {
		t.Error("Payload should not be nil")
	}
}

func TestEnvelope_DecodePayload(t *testing.T) {
	type txPayload struct {
		TransactionID string `json:"transaction_id"`
		Amount        int    `json:"amount"`
	}

	original := txPayload{TransactionID: "txn-789", Amount: 100}
	env, err := NewEnvelope("test.type", "corr-1", original)
	if err != nil {
		t.Fatalf("NewEnvelope returned error: %v", err)
	}

	var decoded txPayload
	if err := env.DecodePayload(&decoded); err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}
	if decoded.TransactionID != "txn-789" {
		t.Errorf("TransactionID = %q, want %q", decoded.TransactionID, "txn-789")
	}
	if decoded.Amount != 100 {
		t.Errorf("Amount = %d, want %d", decoded.Amount, 100)
	}
}

func TestEnvelope_MarshalRoundTrip(t *testing.T) {
	payload := map[string]string{"key": "value"}
	env, err := NewEnvelope("test.roundtrip", "corr-rt", payload)
	if err != nil {
		t.Fatalf("NewEnvelope returned error: %v", err)
	}

	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	restored, err := UnmarshalEnvelope(data)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope returned error: %v", err)
	}

	if restored.MessageID != env.MessageID {
		t.Errorf("MessageID = %q, want %q", restored.MessageID, env.MessageID)
	}
	if restored.CorrelationID != env.CorrelationID {
		t.Errorf("CorrelationID = %q, want %q", restored.CorrelationID, env.CorrelationID)
	}
	if restored.Type != env.Type {
		t.Errorf("Type = %q, want %q", restored.Type, env.Type)
	}
}

func TestUnmarshalEnvelope_InvalidJSON(t *testing.T) {
	_, err := UnmarshalEnvelope([]byte(`{invalid`))
	if err == nil {
		t.Error("UnmarshalEnvelope should return error for invalid JSON")
	}
}

func TestNewEnvelope_InvalidPayload(t *testing.T) {
	// Channels cannot be marshaled to JSON
	_, err := NewEnvelope("test", "corr", make(chan int))
	if err == nil {
		t.Error("NewEnvelope should return error for non-marshallable payload")
	}
}

func TestEnvelope_PayloadIsValidJSON(t *testing.T) {
	payload := map[string]any{"nested": map[string]string{"a": "b"}}
	env, err := NewEnvelope("test", "corr", payload)
	if err != nil {
		t.Fatalf("NewEnvelope returned error: %v", err)
	}

	if !json.Valid(env.Payload) {
		t.Error("Payload should be valid JSON")
	}
}
