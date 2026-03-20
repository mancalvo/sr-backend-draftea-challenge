package consumer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

type ChargeResult = processdeposit.ChargeResult

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func NewConsumerHandler(provider processdeposit.Provider, publisher processdeposit.Publisher, logger *slog.Logger) *Handler {
	return NewHandler(processdeposit.New(provider, publisher, logger), logger)
}

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
	result            *ChargeResult
	err               error
	callCount         int
	uniqueChargeCount int
	seenTransactions  map[string]*ChargeResult
}

func (m *mockProvider) Charge(_ context.Context, transactionID, _ string, _ int64, _ string) (*ChargeResult, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	if m.seenTransactions == nil {
		m.seenTransactions = make(map[string]*ChargeResult)
	}
	if cached, ok := m.seenTransactions[transactionID]; ok {
		result := *cached
		return &result, nil
	}

	m.uniqueChargeCount++
	if m.result == nil {
		m.result = &ChargeResult{Success: true, ProviderRef: "ref-" + transactionID}
	}
	stored := *m.result
	m.seenTransactions[transactionID] = &stored
	return &stored, nil
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

	err := ch.Handle(context.Background(), env)
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

	err := ch.Handle(context.Background(), env)
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

	err := ch.Handle(context.Background(), env)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Should not have published anything on error.
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publish calls on error, got %d", len(pub.calls))
	}
}

func TestHandleDepositRequested_DuplicateDelivery_ReplaysSuccessWithoutExtraCharge(t *testing.T) {
	provider := &mockProvider{
		result: &ChargeResult{Success: true, ProviderRef: "ref-dup"},
	}
	pub := &mockPublisher{}
	ch := NewConsumerHandler(provider, pub, testLogger())

	cmd := messaging.DepositRequested{
		TransactionID: "txn-dup-success",
		UserID:        "user-1",
		Amount:        10000,
		Currency:      "ARS",
	}
	env := makeEnvelope(t, messaging.RoutingKeyDepositRequested, cmd)

	if err := ch.Handle(context.Background(), env); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := ch.Handle(context.Background(), env); err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}

	if provider.uniqueChargeCount != 1 {
		t.Fatalf("unique charges = %d, want 1", provider.uniqueChargeCount)
	}
	if len(pub.calls) != 2 {
		t.Fatalf("publish calls = %d, want 2", len(pub.calls))
	}
	for i, call := range pub.calls {
		if call.RoutingKey != messaging.RoutingKeyProviderChargeSucceeded {
			t.Fatalf("call %d routing key = %q, want %q", i, call.RoutingKey, messaging.RoutingKeyProviderChargeSucceeded)
		}
		outcome, ok := call.Payload.(messaging.ProviderChargeSucceeded)
		if !ok {
			t.Fatalf("call %d payload type = %T, want ProviderChargeSucceeded", i, call.Payload)
		}
		if outcome.ProviderRef != "ref-dup" {
			t.Fatalf("call %d provider_ref = %q, want ref-dup", i, outcome.ProviderRef)
		}
	}
}

func TestHandleDepositRequested_DuplicateDelivery_ReplaysFailureWithoutExtraCharge(t *testing.T) {
	provider := &mockProvider{
		result: &ChargeResult{Success: false, Reason: "card declined"},
	}
	pub := &mockPublisher{}
	ch := NewConsumerHandler(provider, pub, testLogger())

	cmd := messaging.DepositRequested{
		TransactionID: "txn-dup-failure",
		UserID:        "user-1",
		Amount:        10000,
		Currency:      "ARS",
	}
	env := makeEnvelope(t, messaging.RoutingKeyDepositRequested, cmd)

	if err := ch.Handle(context.Background(), env); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := ch.Handle(context.Background(), env); err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}

	if provider.uniqueChargeCount != 1 {
		t.Fatalf("unique charges = %d, want 1", provider.uniqueChargeCount)
	}
	if len(pub.calls) != 2 {
		t.Fatalf("publish calls = %d, want 2", len(pub.calls))
	}
	for i, call := range pub.calls {
		if call.RoutingKey != messaging.RoutingKeyProviderChargeFailed {
			t.Fatalf("call %d routing key = %q, want %q", i, call.RoutingKey, messaging.RoutingKeyProviderChargeFailed)
		}
		outcome, ok := call.Payload.(messaging.ProviderChargeFailed)
		if !ok {
			t.Fatalf("call %d payload type = %T, want ProviderChargeFailed", i, call.Payload)
		}
		if outcome.Reason != "card declined" {
			t.Fatalf("call %d reason = %q, want card declined", i, outcome.Reason)
		}
	}
}

func TestHandle_UnknownMessageType_Ignored(t *testing.T) {
	pub := &mockPublisher{}
	ch := NewConsumerHandler(&mockProvider{}, pub, testLogger())

	env := makeEnvelope(t, "payments.unknown", map[string]string{"status": "noop"})
	if err := ch.Handle(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.calls) != 0 {
		t.Fatalf("publish calls = %d, want 0", len(pub.calls))
	}
}
