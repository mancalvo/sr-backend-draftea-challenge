package payments

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// mockPublisher records publish calls for test verification.
type mockPublisher struct {
	calls []publishCall
}

type publishCall struct {
	Exchange      string
	RoutingKey    string
	CorrelationID string
	Payload       any
}

func (m *mockPublisher) Publish(_ context.Context, exchange, routingKey, correlationID string, payload any) error {
	m.calls = append(m.calls, publishCall{
		Exchange:      exchange,
		RoutingKey:    routingKey,
		CorrelationID: correlationID,
		Payload:       payload,
	})
	return nil
}

// mockProvider is a test provider that returns a configurable result.
type mockProvider struct {
	result *ChargeResult
	err    error
}

func (m *mockProvider) Charge(_ context.Context, _, _ string, _ int64, _ string) (*ChargeResult, error) {
	return m.result, m.err
}

func makeEnvelope(t *testing.T, msgType string, payload any) messaging.Envelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return messaging.Envelope{
		MessageID:     "msg-test",
		CorrelationID: "corr-test",
		Type:          msgType,
		Timestamp:     time.Now().UTC(),
		Payload:       json.RawMessage(data),
	}
}

// --- payments.deposit.requested ---

func TestHandleDepositRequested_ProviderSuccess(t *testing.T) {
	provider := &mockProvider{
		result: &ChargeResult{Success: true, ProviderRef: "ref-123"},
	}
	pub := &mockPublisher{}
	ch := NewConsumerHandler(provider, pub, testLogger())

	cmd := messaging.DepositRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        10000,
		Currency:      "ARS",
	}
	env := makeEnvelope(t, messaging.RoutingKeyDepositRequested, cmd)

	err := ch.HandleDepositRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyProviderChargeSucceeded {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyProviderChargeSucceeded)
	}
	if call.Exchange != messaging.ExchangeOutcomes {
		t.Errorf("exchange = %q, want %q", call.Exchange, messaging.ExchangeOutcomes)
	}
	if call.CorrelationID != "corr-test" {
		t.Errorf("correlation_id = %q, want corr-test", call.CorrelationID)
	}

	outcome, ok := call.Payload.(messaging.ProviderChargeSucceeded)
	if !ok {
		t.Fatalf("payload type = %T, want ProviderChargeSucceeded", call.Payload)
	}
	if outcome.TransactionID != "txn-1" {
		t.Errorf("transaction_id = %v, want txn-1", outcome.TransactionID)
	}
	if outcome.ProviderRef != "ref-123" {
		t.Errorf("provider_ref = %v, want ref-123", outcome.ProviderRef)
	}
}

func TestHandleDepositRequested_ProviderFailure(t *testing.T) {
	provider := &mockProvider{
		result: &ChargeResult{Success: false, Reason: "card declined"},
	}
	pub := &mockPublisher{}
	ch := NewConsumerHandler(provider, pub, testLogger())

	cmd := messaging.DepositRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        10000,
		Currency:      "ARS",
	}
	env := makeEnvelope(t, messaging.RoutingKeyDepositRequested, cmd)

	err := ch.HandleDepositRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyProviderChargeFailed {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyProviderChargeFailed)
	}

	outcome, ok := call.Payload.(messaging.ProviderChargeFailed)
	if !ok {
		t.Fatalf("payload type = %T, want ProviderChargeFailed", call.Payload)
	}
	if outcome.Reason != "card declined" {
		t.Errorf("reason = %v, want 'card declined'", outcome.Reason)
	}
}

func TestHandleDepositRequested_ProviderError(t *testing.T) {
	provider := &mockProvider{
		err: context.DeadlineExceeded,
	}
	pub := &mockPublisher{}
	ch := NewConsumerHandler(provider, pub, testLogger())

	cmd := messaging.DepositRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        10000,
		Currency:      "ARS",
	}
	env := makeEnvelope(t, messaging.RoutingKeyDepositRequested, cmd)

	err := ch.HandleDepositRequested(context.Background(), env)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Should not have published anything on error.
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publish calls on error, got %d", len(pub.calls))
	}
}
