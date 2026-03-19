package wallets

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

// --- wallet.debit.requested ---

func TestHandleWalletDebitRequested_Success(t *testing.T) {
	repo := seedRepo(10000)
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletDebitRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        3000,
		Currency:      "ARS",
		SourceStep:    "purchase_debit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletDebitRequested, cmd)

	err := ch.HandleWalletDebitRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify wallet was debited.
	w, _ := repo.GetWalletByUserID(context.Background(), "user-1")
	if w.Balance != 7000 {
		t.Errorf("balance = %d, want 7000", w.Balance)
	}

	// Verify outcome published.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyWalletDebited {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyWalletDebited)
	}
	if call.Exchange != messaging.ExchangeOutcomes {
		t.Errorf("exchange = %q, want %q", call.Exchange, messaging.ExchangeOutcomes)
	}
	if call.CorrelationID != "corr-test" {
		t.Errorf("correlation_id = %q, want corr-test", call.CorrelationID)
	}

	outcome, ok := call.Payload.(messaging.WalletDebited)
	if !ok {
		t.Fatalf("payload type = %T, want messaging.WalletDebited", call.Payload)
	}
	if outcome.BalanceAfter != 7000 {
		t.Errorf("balance_after = %d, want 7000", outcome.BalanceAfter)
	}
	if outcome.SourceStep != "purchase_debit" {
		t.Errorf("source_step = %v, want purchase_debit", outcome.SourceStep)
	}
}

func TestHandleWalletDebitRequested_InsufficientFunds(t *testing.T) {
	repo := seedRepo(2000)
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletDebitRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        5000,
		Currency:      "ARS",
		SourceStep:    "purchase_debit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletDebitRequested, cmd)

	err := ch.HandleWalletDebitRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Balance should remain unchanged.
	w, _ := repo.GetWalletByUserID(context.Background(), "user-1")
	if w.Balance != 2000 {
		t.Errorf("balance = %d, want 2000", w.Balance)
	}

	// Should publish rejected outcome.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyWalletDebitRejected {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyWalletDebitRejected)
	}

	outcome, ok := call.Payload.(messaging.WalletDebitRejected)
	if !ok {
		t.Fatalf("payload type = %T, want messaging.WalletDebitRejected", call.Payload)
	}
	if outcome.Reason != "insufficient funds" {
		t.Errorf("reason = %v, want 'insufficient funds'", outcome.Reason)
	}
}

func TestHandleWalletDebitRequested_WalletNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletDebitRequested{
		TransactionID: "txn-1",
		UserID:        "nonexistent",
		Amount:        1000,
		Currency:      "ARS",
		SourceStep:    "purchase_debit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletDebitRequested, cmd)

	err := ch.HandleWalletDebitRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should publish rejected outcome.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyWalletDebitRejected {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyWalletDebitRejected)
	}

	outcome, ok := call.Payload.(messaging.WalletDebitRejected)
	if !ok {
		t.Fatalf("payload type = %T, want messaging.WalletDebitRejected", call.Payload)
	}
	if outcome.Reason != "wallet not found" {
		t.Errorf("reason = %v, want 'wallet not found'", outcome.Reason)
	}
}

func TestHandleWalletDebitRequested_IdempotentDuplicate(t *testing.T) {
	repo := seedRepo(10000)
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletDebitRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        3000,
		Currency:      "ARS",
		SourceStep:    "purchase_debit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletDebitRequested, cmd)

	// First debit.
	if err := ch.HandleWalletDebitRequested(context.Background(), env); err != nil {
		t.Fatalf("first debit: %v", err)
	}

	// Second debit with same payload (duplicate delivery).
	pub.calls = nil // reset to check second publish
	if err := ch.HandleWalletDebitRequested(context.Background(), env); err != nil {
		t.Fatalf("second debit: %v", err)
	}

	// Balance should only be debited once.
	w, _ := repo.GetWalletByUserID(context.Background(), "user-1")
	if w.Balance != 7000 {
		t.Errorf("balance after idempotent debit = %d, want 7000", w.Balance)
	}

	// Second call should still publish a success outcome.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call on second invocation, got %d", len(pub.calls))
	}
	if pub.calls[0].RoutingKey != messaging.RoutingKeyWalletDebited {
		t.Errorf("routing key = %q, want %q", pub.calls[0].RoutingKey, messaging.RoutingKeyWalletDebited)
	}
}

// --- wallet.credit.requested ---

func TestHandleWalletCreditRequested_Success(t *testing.T) {
	repo := seedRepo(5000)
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletCreditRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        3000,
		Currency:      "ARS",
		SourceStep:    "deposit_credit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletCreditRequested, cmd)

	err := ch.HandleWalletCreditRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify wallet was credited.
	w, _ := repo.GetWalletByUserID(context.Background(), "user-1")
	if w.Balance != 8000 {
		t.Errorf("balance = %d, want 8000", w.Balance)
	}

	// Verify outcome published.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyWalletCredited {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyWalletCredited)
	}
	if call.Exchange != messaging.ExchangeOutcomes {
		t.Errorf("exchange = %q, want %q", call.Exchange, messaging.ExchangeOutcomes)
	}

	outcome, ok := call.Payload.(messaging.WalletCredited)
	if !ok {
		t.Fatalf("payload type = %T, want messaging.WalletCredited", call.Payload)
	}
	if outcome.BalanceAfter != 8000 {
		t.Errorf("balance_after = %d, want 8000", outcome.BalanceAfter)
	}
	if outcome.SourceStep != "deposit_credit" {
		t.Errorf("source_step = %v, want deposit_credit", outcome.SourceStep)
	}
}

func TestHandleWalletCreditRequested_WalletNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletCreditRequested{
		TransactionID: "txn-1",
		UserID:        "nonexistent",
		Amount:        1000,
		Currency:      "ARS",
		SourceStep:    "deposit_credit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletCreditRequested, cmd)

	err := ch.HandleWalletCreditRequested(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for wallet not found")
	}

	// No outcome should be published for a credit failure on missing wallet
	// (this is an infrastructure error, not a business rejection).
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publish calls, got %d", len(pub.calls))
	}
}

func TestHandleWalletCreditRequested_IdempotentDuplicate(t *testing.T) {
	repo := seedRepo(5000)
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.WalletCreditRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        3000,
		Currency:      "ARS",
		SourceStep:    "deposit_credit",
	}
	env := makeEnvelope(t, messaging.RoutingKeyWalletCreditRequested, cmd)

	// First credit.
	if err := ch.HandleWalletCreditRequested(context.Background(), env); err != nil {
		t.Fatalf("first credit: %v", err)
	}

	// Second credit.
	pub.calls = nil
	if err := ch.HandleWalletCreditRequested(context.Background(), env); err != nil {
		t.Fatalf("second credit: %v", err)
	}

	// Balance should only be credited once.
	w, _ := repo.GetWalletByUserID(context.Background(), "user-1")
	if w.Balance != 8000 {
		t.Errorf("balance after idempotent credit = %d, want 8000", w.Balance)
	}

	// Should still publish success on second call.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call on second invocation, got %d", len(pub.calls))
	}
	if pub.calls[0].RoutingKey != messaging.RoutingKeyWalletCredited {
		t.Errorf("routing key = %q, want %q", pub.calls[0].RoutingKey, messaging.RoutingKeyWalletCredited)
	}
}
