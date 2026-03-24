package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	sagaapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/api"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

type (
	IdempotencyKey              = domain.IdempotencyKey
	MemoryRepository            = repository.MemoryRepository
	PrecheckResult              = client.PrecheckResult
	PurchaseCommand             = sagaapi.PurchaseCommand
	RegisterTransactionRequest  = client.RegisterTransactionRequest
	RegisterTransactionResponse = client.RegisterTransactionResponse
	SagaInstance                = domain.SagaInstance
	SagaOutcome                 = domain.SagaOutcome
	SagaStatus                  = domain.SagaStatus
	TransactionDetails          = client.TransactionDetails
)

const (
	OutcomeCompensated = domain.OutcomeCompensated
	OutcomeFailed      = domain.OutcomeFailed
	OutcomeSucceeded   = domain.OutcomeSucceeded

	SagaTypeDeposit  = domain.SagaTypeDeposit
	SagaTypePurchase = domain.SagaTypePurchase
	SagaTypeRefund   = domain.SagaTypeRefund

	StatusCompensating = domain.StatusCompensating
	StatusCompleted    = domain.StatusCompleted
	StatusCreated      = domain.StatusCreated
	StatusFailed       = domain.StatusFailed
	StatusRunning      = domain.StatusRunning
	StatusTimedOut     = domain.StatusTimedOut
)

type (
	DepositPayload  = workflows.DepositPayload
	PurchasePayload = workflows.PurchasePayload
	RefundPayload   = workflows.RefundPayload
)

var NewMemoryRepository = repository.NewMemoryRepository

func NewConsumerHandler(
	repo repository.Repository,
	paymentsClient client.PaymentsClient,
	publisher activities.Publisher,
	logger *slog.Logger,
) *Handler {
	return NewHandler(repo, paymentsClient, publisher, logger)
}

// ---- Test helpers ----

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type mockCatalogClient struct {
	purchaseResult *PrecheckResult
	purchaseErr    error
	refundResult   *PrecheckResult
	refundErr      error
}

func (m *mockCatalogClient) PurchasePrecheck(_ context.Context, _, _ string) (*PrecheckResult, error) {
	if m.purchaseErr != nil {
		return nil, m.purchaseErr
	}
	if m.purchaseResult == nil {
		return &PrecheckResult{Allowed: true, Price: 5000, Currency: "ARS"}, nil
	}
	result := *m.purchaseResult
	if result.Allowed {
		if result.Price == 0 {
			result.Price = 5000
		}
		if result.Currency == "" {
			result.Currency = "ARS"
		}
	}
	return &result, nil
}

func (m *mockCatalogClient) RefundPrecheck(_ context.Context, _, _, _ string) (*PrecheckResult, error) {
	if m.refundErr != nil {
		return nil, m.refundErr
	}
	if m.refundResult == nil {
		return &PrecheckResult{Allowed: true}, nil
	}
	result := *m.refundResult
	return &result, nil
}

// publishedMessage records a single message published via the mock publisher.
type publishedMessage struct {
	Exchange      string
	RoutingKey    string
	CorrelationID string
	Payload       any
}

// recordingPublisher captures every Publish call for later assertions.
type recordingPublisher struct {
	mu       sync.Mutex
	messages []publishedMessage
}

func (p *recordingPublisher) Publish(_ context.Context, exchange, routingKey, correlationID string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, publishedMessage{
		Exchange:      exchange,
		RoutingKey:    routingKey,
		CorrelationID: correlationID,
		Payload:       payload,
	})
	return nil
}

func (p *recordingPublisher) Messages() []publishedMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]publishedMessage, len(p.messages))
	copy(result, p.messages)
	return result
}

// recordingPaymentsClient captures UpdateTransactionStatus calls.
type recordingPaymentsClient struct {
	mu      sync.Mutex
	updates []statusUpdate
}

type mockPaymentsClient struct {
	registerResult *RegisterTransactionResponse
	registerErr    error
	getResult      *TransactionDetails
	getErr         error
	updateErr      error
	registerCalls  []RegisterTransactionRequest
}

func (m *mockPaymentsClient) RegisterTransaction(_ context.Context, req RegisterTransactionRequest) (*RegisterTransactionResponse, error) {
	m.registerCalls = append(m.registerCalls, req)
	if m.registerErr != nil {
		return nil, m.registerErr
	}
	if m.registerResult != nil {
		return m.registerResult, nil
	}
	return &RegisterTransactionResponse{ID: req.ID, Status: "pending"}, nil
}

func (m *mockPaymentsClient) GetTransaction(_ context.Context, transactionID string) (*TransactionDetails, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResult != nil {
		result := *m.getResult
		if result.ID == "" {
			result.ID = transactionID
		}
		return &result, nil
	}
	offeringID := "offering-1"
	return &TransactionDetails{
		ID:         transactionID,
		UserID:     "user-1",
		Type:       "purchase",
		Status:     "completed",
		Amount:     5000,
		Currency:   "ARS",
		OfferingID: &offeringID,
	}, nil
}

func (m *mockPaymentsClient) UpdateTransactionStatus(_ context.Context, _ string, _ string, _ *string, _ *string) error {
	return m.updateErr
}

type statusUpdate struct {
	TransactionID string
	Status        string
	Reason        *string
	ProviderRef   *string
}

func (c *recordingPaymentsClient) RegisterTransaction(_ context.Context, req RegisterTransactionRequest) (*RegisterTransactionResponse, error) {
	return &RegisterTransactionResponse{ID: req.ID, Status: "pending"}, nil
}

func (c *recordingPaymentsClient) GetTransaction(_ context.Context, transactionID string) (*TransactionDetails, error) {
	offeringID := "offering-1"
	return &TransactionDetails{
		ID:         transactionID,
		UserID:     "user-1",
		Type:       "purchase",
		Status:     "completed",
		Amount:     5000,
		Currency:   "ARS",
		OfferingID: &offeringID,
	}, nil
}

func (c *recordingPaymentsClient) UpdateTransactionStatus(_ context.Context, transactionID string, status string, reason *string, providerReference *string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, statusUpdate{
		TransactionID: transactionID,
		Status:        status,
		Reason:        reason,
		ProviderRef:   providerReference,
	})
	return nil
}

func (c *recordingPaymentsClient) Updates() []statusUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]statusUpdate, len(c.updates))
	copy(result, c.updates)
	return result
}

// createPurchaseSaga sets up a purchase saga in the given repo at the specified status/step.
func createPurchaseSaga(t *testing.T, repo *MemoryRepository, transactionID string, status SagaStatus, step *string) *SagaInstance {
	t.Helper()
	payload, _ := json.Marshal(PurchasePayload{
		UserID:     "user-1",
		OfferingID: "offering-1",
		Amount:     5000,
		Currency:   "ARS",
	})
	timeout := time.Now().UTC().Add(30 * time.Second)
	s, err := repo.CreateSaga(context.Background(), &SagaInstance{
		TransactionID: transactionID,
		Type:          SagaTypePurchase,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &timeout,
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}

	// Advance to target status if needed.
	if status != StatusCreated {
		s, err = repo.UpdateSagaStatus(context.Background(), s.ID, StatusRunning, nil, step)
		if err != nil {
			t.Fatalf("transition to running: %v", err)
		}
		if status != StatusRunning {
			s, err = repo.UpdateSagaStatus(context.Background(), s.ID, status, nil, step)
			if err != nil {
				t.Fatalf("transition to %s: %v", status, err)
			}
		}
	}
	return s
}

// newTestEnvelope creates a messaging.Envelope for testing.
func newTestEnvelope(t *testing.T, msgType string, correlationID string, payload any) messaging.Envelope {
	t.Helper()
	env, err := messaging.NewEnvelope(msgType, correlationID, payload)
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	return env
}

// newHTTPRequest creates a test HTTP POST request.
func newHTTPRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// doRequest executes an HTTP request against a handler and returns the recorder.
func doRequest(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestRouter(handler *sagaapi.Handler) http.Handler {
	r := http.NewServeMux()
	r.HandleFunc("POST /purchases", handler.HandlePurchase)
	return r
}

// ---- Purchase flow tests ----

func TestPurchaseFlow_HappyPath(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-happy"
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	// Step 1: wallet.debited -> should publish access.grant.requested
	env := newTestEnvelope(t, messaging.RoutingKeyWalletDebited, txnID, messaging.WalletDebited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  15000,
		SourceStep:    "purchase_debit",
	})
	if err := handler.Handle(ctx, env); err != nil {
		t.Fatalf("handleWalletDebited: %v", err)
	}

	// Verify access.grant.requested was published.
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyAccessGrantRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyAccessGrantRequested)
	}
	if msgs[0].Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", msgs[0].Exchange, messaging.ExchangeCommands)
	}

	// Verify saga is still running (step only changes on status transitions).
	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", updated.Status)
	}

	// Step 2: access.granted -> should complete the saga.
	env = newTestEnvelope(t, messaging.RoutingKeyAccessGranted, txnID, messaging.AccessGranted{
		TransactionID: txnID,
		UserID:        "user-1",
		OfferingID:    "offering-1",
	})
	if err := handler.Handle(ctx, env); err != nil {
		t.Fatalf("handleAccessGranted: %v", err)
	}

	// Verify saga is completed with succeeded outcome.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "purchase_completed" {
		t.Errorf("current_step = %v, want purchase_completed", final.CurrentStep)
	}

	// Verify transaction was marked completed.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].TransactionID != txnID {
		t.Errorf("transaction_id = %s, want %s", updates[0].TransactionID, txnID)
	}
	if updates[0].Status != "completed" {
		t.Errorf("status = %s, want completed", updates[0].Status)
	}
}

func TestPurchaseFlow_InsufficientFunds(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-insuf"
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	// wallet.debit.rejected -> should fail the saga.
	env := newTestEnvelope(t, messaging.RoutingKeyWalletDebitRejected, txnID, messaging.WalletDebitRejected{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		Reason:        "insufficient funds",
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletDebitRejected: %v", err)
	}

	// Verify saga is failed.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusFailed {
		t.Errorf("saga status = %s, want failed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeFailed {
		t.Errorf("saga outcome = %v, want failed", final.Outcome)
	}

	// Verify transaction was marked failed.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].Status != "failed" {
		t.Errorf("status = %s, want failed", updates[0].Status)
	}
	if updates[0].Reason == nil {
		t.Fatal("expected status reason, got nil")
	}

	// No commands should have been published.
	if len(pub.Messages()) != 0 {
		t.Errorf("expected no published messages, got %d", len(pub.Messages()))
	}
}

func TestPurchaseFlow_DuplicateWalletOutcomeRedelivery(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-dup"
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	// First delivery of wallet.debited.
	env := newTestEnvelope(t, messaging.RoutingKeyWalletDebited, txnID, messaging.WalletDebited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  15000,
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("first handleWalletDebited: %v", err)
	}

	// Should have published one access.grant.requested.
	if len(pub.Messages()) != 1 {
		t.Fatalf("expected 1 message after first delivery, got %d", len(pub.Messages()))
	}
	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.CurrentStep == nil || *updated.CurrentStep != "purchase_grant" {
		t.Fatalf("current_step = %v, want purchase_grant", updated.CurrentStep)
	}

	// Second delivery (duplicate) of the same wallet.debited.
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("second handleWalletDebited: %v", err)
	}
	if len(pub.Messages()) != 1 {
		t.Fatalf("expected duplicate wallet.debited to be ignored, got %d messages", len(pub.Messages()))
	}

	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", final.Status)
	}

	// Now complete the saga via access.granted.
	env = newTestEnvelope(t, messaging.RoutingKeyAccessGranted, txnID, messaging.AccessGranted{
		TransactionID: txnID,
		UserID:        "user-1",
		OfferingID:    "offering-1",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleAccessGranted: %v", err)
	}

	final, _ = repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}

	// Now deliver wallet.debited again after completion -> should be ignored.
	env = newTestEnvelope(t, messaging.RoutingKeyWalletDebited, txnID, messaging.WalletDebited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  15000,
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("third handleWalletDebited (after completion): %v", err)
	}

	// Saga should still be completed.
	final, _ = repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("after duplicate on completed saga: status = %s, want completed", final.Status)
	}
}

func TestPurchaseFlow_StaleDebitRejectedAfterGrantIgnored(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-stale-reject"
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	debited := newTestEnvelope(t, messaging.RoutingKeyWalletDebited, txnID, messaging.WalletDebited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  15000,
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, debited); err != nil {
		t.Fatalf("handleWalletDebited: %v", err)
	}

	rejected := newTestEnvelope(t, messaging.RoutingKeyWalletDebitRejected, txnID, messaging.WalletDebitRejected{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		Reason:        "insufficient funds",
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, rejected); err != nil {
		t.Fatalf("handleWalletDebitRejected: %v", err)
	}

	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Fatalf("status = %s, want running", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != "purchase_grant" {
		t.Fatalf("current_step = %v, want purchase_grant", updated.CurrentStep)
	}
	if len(payments.Updates()) != 0 {
		t.Fatalf("expected stale rejection not to update payments, got %d updates", len(payments.Updates()))
	}
}

func TestPurchaseFlow_AccessConflictAfterDebit_Compensation(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-comp"
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	// Step 1: wallet.debited -> publish access.grant.requested.
	env := newTestEnvelope(t, messaging.RoutingKeyWalletDebited, txnID, messaging.WalletDebited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  15000,
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletDebited: %v", err)
	}

	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyAccessGrantRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyAccessGrantRequested)
	}

	// Step 2: access.grant.conflicted -> should publish wallet.credit.requested (compensation).
	env = newTestEnvelope(t, messaging.RoutingKeyAccessGrantConflicted, txnID, messaging.AccessGrantConflicted{
		TransactionID: txnID,
		UserID:        "user-1",
		OfferingID:    "offering-1",
		Reason:        "user already has active access",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleAccessGrantConflicted: %v", err)
	}

	// Verify saga is compensating.
	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusCompensating {
		t.Errorf("saga status = %s, want compensating", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != "purchase_compensation_credit" {
		t.Errorf("current_step = %v, want purchase_compensation_credit", updated.CurrentStep)
	}

	// Verify wallet.credit.requested was published.
	msgs = pub.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 published messages, got %d", len(msgs))
	}
	creditMsg := msgs[1]
	if creditMsg.RoutingKey != messaging.RoutingKeyWalletCreditRequested {
		t.Errorf("routing key = %s, want %s", creditMsg.RoutingKey, messaging.RoutingKeyWalletCreditRequested)
	}
	if creditMsg.Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", creditMsg.Exchange, messaging.ExchangeCommands)
	}

	// Verify the credit payload has the right source_step.
	creditPayload, ok := creditMsg.Payload.(messaging.WalletCreditRequested)
	if !ok {
		t.Fatalf("expected WalletCreditRequested payload, got %T", creditMsg.Payload)
	}
	if creditPayload.SourceStep != "purchase_compensation" {
		t.Errorf("source_step = %s, want purchase_compensation", creditPayload.SourceStep)
	}
	if creditPayload.Amount != 5000 {
		t.Errorf("credit amount = %d, want 5000", creditPayload.Amount)
	}

	// Step 3: wallet.credited -> should complete the saga as compensated.
	env = newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  20000,
		SourceStep:    "purchase_compensation",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletCredited: %v", err)
	}

	// Verify saga is completed with compensated outcome.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeCompensated {
		t.Errorf("saga outcome = %v, want compensated", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "purchase_compensation_credited" {
		t.Errorf("current_step = %v, want purchase_compensation_credited", final.CurrentStep)
	}

	// Verify transaction was marked compensated.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].Status != "compensated" {
		t.Errorf("status = %s, want compensated", updates[0].Status)
	}
	if updates[0].Reason == nil {
		t.Fatal("expected status reason, got nil")
	}
}

// TestPurchaseFlow_DuplicateAccessGrantConflicted verifies that a duplicate
// access.grant.conflicted event on a saga already in compensating state is
// handled idempotently.
func TestPurchaseFlow_DuplicateAccessGrantConflicted(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-dup-conflict"
	step := "purchase_grant"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	// First access.grant.conflicted -> transitions to compensating.
	env := newTestEnvelope(t, messaging.RoutingKeyAccessGrantConflicted, txnID, messaging.AccessGrantConflicted{
		TransactionID: txnID,
		UserID:        "user-1",
		OfferingID:    "offering-1",
		Reason:        "duplicate access",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("first access.grant.conflicted: %v", err)
	}

	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusCompensating {
		t.Fatalf("saga status = %s, want compensating", updated.Status)
	}

	msgsBefore := len(pub.Messages())

	// Second delivery (duplicate) -> should be ignored.
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("second access.grant.conflicted: %v", err)
	}

	// No additional messages should have been published.
	msgsAfter := len(pub.Messages())
	if msgsAfter != msgsBefore {
		t.Errorf("expected no new messages on duplicate, got %d new", msgsAfter-msgsBefore)
	}

	// Saga should still be compensating.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompensating {
		t.Errorf("saga status = %s, want compensating", final.Status)
	}
}

// TestPurchaseFlow_WalletCreditedOnTerminalSaga verifies that a wallet.credited
// event on an already completed saga is handled idempotently.
func TestPurchaseFlow_WalletCreditedOnTerminalSaga(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-credit-dup"

	// Create saga and advance to compensating, then completed.
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	compStep := "purchase_compensation_credit"
	_, err := repo.UpdateSagaStatus(ctx, s.ID, StatusCompensating, nil, &compStep)
	if err != nil {
		t.Fatalf("transition to compensating: %v", err)
	}

	outcome := OutcomeCompensated
	finalStep := "purchase_compensation_credited"
	_, err = repo.UpdateSagaStatus(ctx, s.ID, StatusCompleted, &outcome, &finalStep)
	if err != nil {
		t.Fatalf("transition to completed: %v", err)
	}

	// Deliver wallet.credited again -> should be silently ignored.
	env := newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  20000,
		SourceStep:    "purchase_compensation",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletCredited on completed saga: %v", err)
	}

	// No status updates should have been made.
	if len(payments.Updates()) != 0 {
		t.Errorf("expected no status updates on duplicate, got %d", len(payments.Updates()))
	}

	// Saga should still be completed.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
}

// TestPurchaseFlow_DebitRejectedOnTerminalSaga verifies duplicate debit rejected
// events after saga has already failed.
func TestPurchaseFlow_DebitRejectedOnTerminalSaga(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-debit-rej-dup"
	step := "purchase_debit"
	s := createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	// First debit rejected.
	env := newTestEnvelope(t, messaging.RoutingKeyWalletDebitRejected, txnID, messaging.WalletDebitRejected{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		Reason:        "insufficient funds",
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("first debit rejected: %v", err)
	}

	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusFailed {
		t.Fatalf("saga status = %s, want failed", final.Status)
	}

	updatesBefore := len(payments.Updates())

	// Second delivery (duplicate) -> should be ignored.
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("second debit rejected: %v", err)
	}

	// No additional updates.
	if len(payments.Updates()) != updatesBefore {
		t.Errorf("expected no new updates on duplicate, got %d new", len(payments.Updates())-updatesBefore)
	}
}

// TestPurchaseFlow_HandlerPurchaseInitiatesWorkflow verifies that HandlePurchase
// transitions the saga to running and publishes wallet.debit.requested.
func TestPurchaseFlow_HandlerPurchaseInitiatesWorkflow(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	payments := &mockPaymentsClient{}
	h := sagaapi.NewHandler(repo, catalog, payments, pub, 30*time.Second, discardLogger(), false)
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: fmt.Sprintf("pur-init-%d", time.Now().UnixNano()),
	})

	req := newHTTPRequest(t, "POST", "/purchases", body)
	rec := doRequest(router, req)

	if rec.Code != 202 {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}

	// Extract transaction ID from response.
	var resp struct {
		Data struct {
			TransactionID string `json:"transaction_id"`
		} `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	txnID := resp.Data.TransactionID
	if txnID == "" {
		t.Fatal("empty transaction_id in response")
	}

	// Verify saga was created and transitioned to running.
	s, err := repo.GetSagaByTransactionID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if s.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", s.Status)
	}
	if s.CurrentStep == nil || *s.CurrentStep != "purchase_debit" {
		t.Errorf("current_step = %v, want purchase_debit", s.CurrentStep)
	}

	// Verify wallet.debit.requested was published.
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyWalletDebitRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyWalletDebitRequested)
	}
	if msgs[0].Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", msgs[0].Exchange, messaging.ExchangeCommands)
	}

	debitPayload, ok := msgs[0].Payload.(messaging.WalletDebitRequested)
	if !ok {
		t.Fatalf("expected WalletDebitRequested payload, got %T", msgs[0].Payload)
	}
	if debitPayload.TransactionID != txnID {
		t.Errorf("debit transaction_id = %s, want %s", debitPayload.TransactionID, txnID)
	}
	if debitPayload.UserID != "user-1" {
		t.Errorf("debit user_id = %s, want user-1", debitPayload.UserID)
	}
	if debitPayload.Amount != 5000 {
		t.Errorf("debit amount = %d, want 5000", debitPayload.Amount)
	}
	if debitPayload.SourceStep != "purchase_debit" {
		t.Errorf("debit source_step = %s, want purchase_debit", debitPayload.SourceStep)
	}
}

// ---- Refund flow helpers ----

// createRefundSaga sets up a refund saga in the given repo at the specified status/step.
func createRefundSaga(t *testing.T, repo *MemoryRepository, transactionID string, status SagaStatus, step *string) *SagaInstance {
	t.Helper()
	payload, _ := json.Marshal(RefundPayload{
		UserID:              "user-1",
		OfferingID:          "offering-1",
		OriginalTransaction: "orig-purchase-txn",
		Amount:              5000,
		Currency:            "ARS",
	})
	timeout := time.Now().UTC().Add(30 * time.Second)
	s, err := repo.CreateSaga(context.Background(), &SagaInstance{
		TransactionID: transactionID,
		Type:          SagaTypeRefund,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &timeout,
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}

	// Advance to target status if needed.
	if status != StatusCreated {
		s, err = repo.UpdateSagaStatus(context.Background(), s.ID, StatusRunning, nil, step)
		if err != nil {
			t.Fatalf("transition to running: %v", err)
		}
		if status != StatusRunning {
			s, err = repo.UpdateSagaStatus(context.Background(), s.ID, status, nil, step)
			if err != nil {
				t.Fatalf("transition to %s: %v", status, err)
			}
		}
	}
	return s
}

// failingPaymentsClient fails the first N UpdateTransactionStatus calls, then succeeds.
type failingPaymentsClient struct {
	mu           sync.Mutex
	failCount    int
	callCount    int
	updates      []statusUpdate
	registerResp *RegisterTransactionResponse
}

func (c *failingPaymentsClient) RegisterTransaction(_ context.Context, req RegisterTransactionRequest) (*RegisterTransactionResponse, error) {
	if c.registerResp != nil {
		return c.registerResp, nil
	}
	return &RegisterTransactionResponse{ID: req.ID, Status: "pending"}, nil
}

func (c *failingPaymentsClient) GetTransaction(_ context.Context, transactionID string) (*TransactionDetails, error) {
	offeringID := "offering-1"
	return &TransactionDetails{
		ID:         transactionID,
		UserID:     "user-1",
		Type:       "purchase",
		Status:     "completed",
		Amount:     5000,
		Currency:   "ARS",
		OfferingID: &offeringID,
	}, nil
}

func (c *failingPaymentsClient) UpdateTransactionStatus(_ context.Context, transactionID string, status string, reason *string, providerReference *string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callCount++
	if c.callCount <= c.failCount {
		return fmt.Errorf("temporary failure (call %d/%d)", c.callCount, c.failCount)
	}
	c.updates = append(c.updates, statusUpdate{
		TransactionID: transactionID,
		Status:        status,
		Reason:        reason,
		ProviderRef:   providerReference,
	})
	return nil
}

func (c *failingPaymentsClient) Updates() []statusUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]statusUpdate, len(c.updates))
	copy(result, c.updates)
	return result
}

// ---- Refund flow tests ----

func TestRefundFlow_HappyPath(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-refund-happy"
	step := "refund_revoke_access"
	s := createRefundSaga(t, repo, txnID, StatusRunning, &step)

	// Step 1: access.revoked -> should publish wallet.credit.requested.
	// Note: TransactionID in the payload is the original purchase txn,
	// but CorrelationID is the refund txn ID.
	env := newTestEnvelope(t, messaging.RoutingKeyAccessRevoked, txnID, messaging.AccessRevoked{
		TransactionID:         txnID,
		OriginalTransactionID: "orig-purchase-txn",
		UserID:                "user-1",
		OfferingID:            "offering-1",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleAccessRevoked: %v", err)
	}

	// Verify wallet.credit.requested was published.
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyWalletCreditRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyWalletCreditRequested)
	}
	if msgs[0].Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", msgs[0].Exchange, messaging.ExchangeCommands)
	}

	// Verify the credit payload.
	creditPayload, ok := msgs[0].Payload.(messaging.WalletCreditRequested)
	if !ok {
		t.Fatalf("expected WalletCreditRequested payload, got %T", msgs[0].Payload)
	}
	if creditPayload.TransactionID != txnID {
		t.Errorf("credit transaction_id = %s, want %s", creditPayload.TransactionID, txnID)
	}
	if creditPayload.UserID != "user-1" {
		t.Errorf("credit user_id = %s, want user-1", creditPayload.UserID)
	}
	if creditPayload.Amount != 5000 {
		t.Errorf("credit amount = %d, want 5000", creditPayload.Amount)
	}
	if creditPayload.SourceStep != "refund_credit" {
		t.Errorf("credit source_step = %s, want refund_credit", creditPayload.SourceStep)
	}

	// Verify saga is still running (step update happens on final transition).
	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", updated.Status)
	}

	// Step 2: wallet.credited -> should complete the refund saga.
	env = newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  25000,
		SourceStep:    "refund_credit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletCredited: %v", err)
	}

	// Verify saga is completed with succeeded outcome.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "refund_completed" {
		t.Errorf("current_step = %v, want refund_completed", final.CurrentStep)
	}

	// Verify transaction was marked completed.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].TransactionID != txnID {
		t.Errorf("transaction_id = %s, want %s", updates[0].TransactionID, txnID)
	}
	if updates[0].Status != "completed" {
		t.Errorf("status = %s, want completed", updates[0].Status)
	}
}

func TestRefundFlow_RevokeRejectedAccessInactive(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-refund-revoke-rej"
	step := "refund_revoke_access"
	s := createRefundSaga(t, repo, txnID, StatusRunning, &step)

	// access.revoke.rejected because access is already inactive.
	env := newTestEnvelope(t, messaging.RoutingKeyAccessRevokeRejected, txnID, messaging.AccessRevokeRejected{
		TransactionID:         txnID,
		OriginalTransactionID: "orig-purchase-txn",
		UserID:                "user-1",
		OfferingID:            "offering-1",
		Reason:                "no active access found for this transaction",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleAccessRevokeRejected: %v", err)
	}

	// Verify saga is failed.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusFailed {
		t.Errorf("saga status = %s, want failed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeFailed {
		t.Errorf("saga outcome = %v, want failed", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "refund_revoke_rejected" {
		t.Errorf("current_step = %v, want refund_revoke_rejected", final.CurrentStep)
	}

	// Verify transaction was marked failed.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].Status != "failed" {
		t.Errorf("status = %s, want failed", updates[0].Status)
	}
	if updates[0].Reason == nil {
		t.Fatal("expected status reason, got nil")
	}

	// No credit commands should have been published.
	if len(pub.Messages()) != 0 {
		t.Errorf("expected no published messages, got %d", len(pub.Messages()))
	}
}

func TestRefundFlow_DuplicateRevokeEventHandling(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-refund-dup-revoke"
	step := "refund_revoke_access"
	s := createRefundSaga(t, repo, txnID, StatusRunning, &step)

	// First delivery of access.revoked -> should publish wallet.credit.requested.
	env := newTestEnvelope(t, messaging.RoutingKeyAccessRevoked, txnID, messaging.AccessRevoked{
		TransactionID:         txnID,
		OriginalTransactionID: "orig-purchase-txn",
		UserID:                "user-1",
		OfferingID:            "offering-1",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("first handleAccessRevoked: %v", err)
	}

	if len(pub.Messages()) != 1 {
		t.Fatalf("expected 1 message after first delivery, got %d", len(pub.Messages()))
	}
	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.CurrentStep == nil || *updated.CurrentStep != "refund_credit" {
		t.Fatalf("current_step = %v, want refund_credit", updated.CurrentStep)
	}

	// Second delivery (duplicate) of access.revoked -> should not error.
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("second handleAccessRevoked: %v", err)
	}
	if len(pub.Messages()) != 1 {
		t.Fatalf("expected duplicate access.revoked to be ignored, got %d messages", len(pub.Messages()))
	}

	// Saga should still be running.
	updated, _ = repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", updated.Status)
	}

	// Now complete the saga via wallet.credited.
	creditEnv := newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  25000,
		SourceStep:    "refund_credit",
	})
	if err := handler.HandleOutcome(ctx, creditEnv); err != nil {
		t.Fatalf("handleWalletCredited: %v", err)
	}

	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}

	// Deliver access.revoked again after completion -> should be silently ignored.
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("third handleAccessRevoked (after completion): %v", err)
	}

	final, _ = repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("after duplicate on completed saga: status = %s, want completed", final.Status)
	}
}

func TestRefundFlow_StaleRevokeRejectedAfterCreditIgnored(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-refund-stale-reject"
	step := "refund_revoke_access"
	s := createRefundSaga(t, repo, txnID, StatusRunning, &step)

	revoked := newTestEnvelope(t, messaging.RoutingKeyAccessRevoked, txnID, messaging.AccessRevoked{
		TransactionID:         txnID,
		OriginalTransactionID: "orig-purchase-txn",
		UserID:                "user-1",
		OfferingID:            "offering-1",
	})
	if err := handler.HandleOutcome(ctx, revoked); err != nil {
		t.Fatalf("handleAccessRevoked: %v", err)
	}

	rejected := newTestEnvelope(t, messaging.RoutingKeyAccessRevokeRejected, txnID, messaging.AccessRevokeRejected{
		TransactionID:         txnID,
		OriginalTransactionID: "orig-purchase-txn",
		UserID:                "user-1",
		OfferingID:            "offering-1",
		Reason:                "no active access found",
	})
	if err := handler.HandleOutcome(ctx, rejected); err != nil {
		t.Fatalf("handleAccessRevokeRejected: %v", err)
	}

	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Fatalf("status = %s, want running", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != "refund_credit" {
		t.Fatalf("current_step = %v, want refund_credit", updated.CurrentStep)
	}
	if len(payments.Updates()) != 0 {
		t.Fatalf("expected stale revoke rejection not to update payments, got %d updates", len(payments.Updates()))
	}
}

func TestRefundFlow_RetryPathFinalStatusUpdateFails(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &failingPaymentsClient{failCount: 1}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-refund-retry"
	step := "refund_credit"
	s := createRefundSaga(t, repo, txnID, StatusRunning, &step)

	// wallet.credited arrives but the final status update fails on first attempt.
	env := newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		BalanceAfter:  25000,
		SourceStep:    "refund_credit",
	})

	// First attempt: should return an error (payments client fails).
	err := handler.HandleOutcome(ctx, env)
	if err == nil {
		t.Fatal("expected error on first attempt when payments client fails")
	}

	// The saga should still be running so a retry can repair the finalization.
	mid, _ := repo.GetSagaByID(ctx, s.ID)
	if mid.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", mid.Status)
	}

	// Second attempt (retry): payments update succeeds and the saga can complete.
	err = handler.HandleOutcome(ctx, env)
	if err != nil {
		t.Fatalf("expected nil on retry, got: %v", err)
	}

	// Verify the retry repaired the partial progress and completed the saga.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", final.Outcome)
	}
	if len(payments.Updates()) != 1 {
		t.Fatalf("payments updates = %d, want 1", len(payments.Updates()))
	}
}

// ---- Deposit flow helpers ----

// createDepositSaga sets up a deposit saga in the given repo at the specified status/step.
func createDepositSaga(t *testing.T, repo *MemoryRepository, transactionID string, status SagaStatus, step *string) *SagaInstance {
	t.Helper()
	payload, _ := json.Marshal(DepositPayload{
		UserID:   "user-1",
		Amount:   10000,
		Currency: "ARS",
	})
	timeout := time.Now().UTC().Add(30 * time.Second)
	s, err := repo.CreateSaga(context.Background(), &SagaInstance{
		TransactionID: transactionID,
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &timeout,
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}

	// Advance to target status if needed.
	if status != StatusCreated {
		s, err = repo.UpdateSagaStatus(context.Background(), s.ID, StatusRunning, nil, step)
		if err != nil {
			t.Fatalf("transition to running: %v", err)
		}
		if status != StatusRunning {
			s, err = repo.UpdateSagaStatus(context.Background(), s.ID, status, nil, step)
			if err != nil {
				t.Fatalf("transition to %s: %v", status, err)
			}
		}
	}
	return s
}

// ---- Deposit flow tests ----

func TestDepositFlow_HappyPath(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-deposit-happy"
	step := "deposit_charge"
	s := createDepositSaga(t, repo, txnID, StatusRunning, &step)

	// Step 1: provider.charge.succeeded -> should publish wallet.credit.requested.
	env := newTestEnvelope(t, messaging.RoutingKeyProviderChargeSucceeded, txnID, messaging.ProviderChargeSucceeded{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		ProviderRef:   "sim-" + txnID,
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleProviderChargeSucceeded: %v", err)
	}

	// Verify wallet.credit.requested was published.
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyWalletCreditRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyWalletCreditRequested)
	}
	if msgs[0].Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", msgs[0].Exchange, messaging.ExchangeCommands)
	}

	creditPayload, ok := msgs[0].Payload.(messaging.WalletCreditRequested)
	if !ok {
		t.Fatalf("expected WalletCreditRequested payload, got %T", msgs[0].Payload)
	}
	if creditPayload.TransactionID != txnID {
		t.Errorf("credit transaction_id = %s, want %s", creditPayload.TransactionID, txnID)
	}
	if creditPayload.UserID != "user-1" {
		t.Errorf("credit user_id = %s, want user-1", creditPayload.UserID)
	}
	if creditPayload.Amount != 10000 {
		t.Errorf("credit amount = %d, want 10000", creditPayload.Amount)
	}
	if creditPayload.SourceStep != "deposit_credit" {
		t.Errorf("credit source_step = %s, want deposit_credit", creditPayload.SourceStep)
	}

	// Verify saga is still running (not yet completed).
	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != "deposit_credit" {
		t.Errorf("current_step = %v, want deposit_credit", updated.CurrentStep)
	}

	// Step 2: wallet.credited -> should complete the saga.
	env = newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		BalanceAfter:  20000,
		SourceStep:    "deposit_credit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletCredited: %v", err)
	}

	// Verify saga is completed with succeeded outcome.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "deposit_completed" {
		t.Errorf("current_step = %v, want deposit_completed", final.CurrentStep)
	}

	// Verify transaction was marked completed.
	updates := payments.Updates()
	if len(updates) != 2 {
		t.Fatalf("expected 2 status updates, got %d", len(updates))
	}
	if updates[0].TransactionID != txnID {
		t.Errorf("transaction_id = %s, want %s", updates[0].TransactionID, txnID)
	}
	if updates[0].Status != "pending" {
		t.Errorf("status = %s, want pending", updates[0].Status)
	}
	if updates[0].ProviderRef == nil || *updates[0].ProviderRef != "sim-"+txnID {
		t.Errorf("provider_ref = %v, want %q", updates[0].ProviderRef, "sim-"+txnID)
	}
	if updates[1].Status != "completed" {
		t.Errorf("status = %s, want completed", updates[1].Status)
	}
	if updates[1].ProviderRef != nil {
		t.Errorf("completed provider_ref = %v, want nil", updates[1].ProviderRef)
	}
}

func TestDepositFlow_ProviderFailure(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-deposit-fail"
	step := "deposit_charge"
	s := createDepositSaga(t, repo, txnID, StatusRunning, &step)

	// provider.charge.failed -> should fail the saga.
	env := newTestEnvelope(t, messaging.RoutingKeyProviderChargeFailed, txnID, messaging.ProviderChargeFailed{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		Reason:        "card declined",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleProviderChargeFailed: %v", err)
	}

	// Verify saga is failed.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusFailed {
		t.Errorf("saga status = %s, want failed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeFailed {
		t.Errorf("saga outcome = %v, want failed", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "deposit_charge_failed" {
		t.Errorf("current_step = %v, want deposit_charge_failed", final.CurrentStep)
	}

	// Verify transaction was marked failed.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].Status != "failed" {
		t.Errorf("status = %s, want failed", updates[0].Status)
	}
	if updates[0].Reason == nil {
		t.Fatal("expected status reason, got nil")
	}

	// No commands should have been published (no wallet credit).
	if len(pub.Messages()) != 0 {
		t.Errorf("expected no published messages, got %d", len(pub.Messages()))
	}
}

func TestDepositFlow_ProviderTimeout(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-deposit-timeout"
	step := "deposit_charge"
	// Create a deposit saga already timed_out by the timeout poller.
	s := createDepositSaga(t, repo, txnID, StatusTimedOut, &step)

	// Verify saga is timed_out.
	current, _ := repo.GetSagaByID(ctx, s.ID)
	if current.Status != StatusTimedOut {
		t.Fatalf("saga status = %s, want timed_out", current.Status)
	}

	// A late provider.charge.failed arrives after timeout -> should fail the saga.
	env := newTestEnvelope(t, messaging.RoutingKeyProviderChargeFailed, txnID, messaging.ProviderChargeFailed{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		Reason:        "provider timeout",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleProviderChargeFailed on timed_out saga: %v", err)
	}

	// Verify saga transitioned from timed_out to failed.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusFailed {
		t.Errorf("saga status = %s, want failed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeFailed {
		t.Errorf("saga outcome = %v, want failed", final.Outcome)
	}

	// Verify transaction was updated to failed.
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].Status != "failed" {
		t.Errorf("status = %s, want failed", updates[0].Status)
	}
}

func TestDepositFlow_LateSuccessAfterTimeout(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-deposit-late-success"
	step := "deposit_charge"
	// Create a deposit saga already timed_out.
	s := createDepositSaga(t, repo, txnID, StatusTimedOut, &step)

	// Late provider.charge.succeeded arrives after timeout.
	env := newTestEnvelope(t, messaging.RoutingKeyProviderChargeSucceeded, txnID, messaging.ProviderChargeSucceeded{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		ProviderRef:   "sim-" + txnID,
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleProviderChargeSucceeded on timed_out saga: %v", err)
	}

	// Verify wallet.credit.requested was published (late success resumes the flow).
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyWalletCreditRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyWalletCreditRequested)
	}

	creditPayload, ok := msgs[0].Payload.(messaging.WalletCreditRequested)
	if !ok {
		t.Fatalf("expected WalletCreditRequested payload, got %T", msgs[0].Payload)
	}
	if creditPayload.SourceStep != "deposit_credit" {
		t.Errorf("source_step = %s, want deposit_credit", creditPayload.SourceStep)
	}

	// Step 2: wallet.credited -> should complete the saga from timed_out state.
	env = newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		BalanceAfter:  20000,
		SourceStep:    "deposit_credit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletCredited on timed_out saga: %v", err)
	}

	// Verify saga is completed with succeeded outcome.
	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("saga status = %s, want completed", final.Status)
	}
	if final.Outcome == nil || *final.Outcome != OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", final.Outcome)
	}
	if final.CurrentStep == nil || *final.CurrentStep != "deposit_completed" {
		t.Errorf("current_step = %v, want deposit_completed", final.CurrentStep)
	}

	// Verify transaction was marked completed.
	updates := payments.Updates()
	if len(updates) != 2 {
		t.Fatalf("expected 2 status updates, got %d", len(updates))
	}
	if updates[0].Status != "timed_out" {
		t.Errorf("status = %s, want timed_out", updates[0].Status)
	}
	if updates[0].ProviderRef == nil || *updates[0].ProviderRef != "sim-"+txnID {
		t.Errorf("provider_ref = %v, want %q", updates[0].ProviderRef, "sim-"+txnID)
	}
	if updates[1].Status != "completed" {
		t.Errorf("status = %s, want completed", updates[1].Status)
	}
}

func TestDepositFlow_StaleProviderFailureAfterSuccessIgnored(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-deposit-stale-failure"
	step := "deposit_charge"
	s := createDepositSaga(t, repo, txnID, StatusRunning, &step)

	succeeded := newTestEnvelope(t, messaging.RoutingKeyProviderChargeSucceeded, txnID, messaging.ProviderChargeSucceeded{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		ProviderRef:   "sim-" + txnID,
	})
	if err := handler.HandleOutcome(ctx, succeeded); err != nil {
		t.Fatalf("handleProviderChargeSucceeded: %v", err)
	}

	failed := newTestEnvelope(t, messaging.RoutingKeyProviderChargeFailed, txnID, messaging.ProviderChargeFailed{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		Reason:        "provider timeout",
	})
	if err := handler.HandleOutcome(ctx, failed); err != nil {
		t.Fatalf("handleProviderChargeFailed: %v", err)
	}

	updated, _ := repo.GetSagaByID(ctx, s.ID)
	if updated.Status != StatusRunning {
		t.Fatalf("status = %s, want running", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != "deposit_credit" {
		t.Fatalf("current_step = %v, want deposit_credit", updated.CurrentStep)
	}

	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected only provider success update, got %d updates", len(updates))
	}
	if updates[0].Status != "pending" {
		t.Fatalf("status = %s, want pending", updates[0].Status)
	}
}

func TestDepositFlow_DuplicateProviderCallbackDedupe(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-deposit-dup-callback"
	step := "deposit_charge"
	s := createDepositSaga(t, repo, txnID, StatusRunning, &step)

	// First delivery: provider.charge.succeeded -> publishes wallet.credit.requested.
	env := newTestEnvelope(t, messaging.RoutingKeyProviderChargeSucceeded, txnID, messaging.ProviderChargeSucceeded{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		ProviderRef:   "sim-" + txnID,
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("first handleProviderChargeSucceeded: %v", err)
	}

	if len(pub.Messages()) != 1 {
		t.Fatalf("expected 1 message after first delivery, got %d", len(pub.Messages()))
	}

	// wallet.credited -> completes the saga.
	creditEnv := newTestEnvelope(t, messaging.RoutingKeyWalletCredited, txnID, messaging.WalletCredited{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        10000,
		BalanceAfter:  20000,
		SourceStep:    "deposit_credit",
	})
	if err := handler.HandleOutcome(ctx, creditEnv); err != nil {
		t.Fatalf("handleWalletCredited: %v", err)
	}

	final, _ := repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("saga status = %s, want completed", final.Status)
	}

	updatesBefore := len(payments.Updates())
	msgsBefore := len(pub.Messages())

	// Second delivery (duplicate) of provider.charge.succeeded -> should be ignored.
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("second handleProviderChargeSucceeded: %v", err)
	}

	// No new messages or status updates.
	if len(pub.Messages()) != msgsBefore {
		t.Errorf("expected no new messages on duplicate, got %d new", len(pub.Messages())-msgsBefore)
	}
	if len(payments.Updates()) != updatesBefore {
		t.Errorf("expected no new status updates on duplicate, got %d new", len(payments.Updates())-updatesBefore)
	}

	// Saga should still be completed.
	final, _ = repo.GetSagaByID(ctx, s.ID)
	if final.Status != StatusCompleted {
		t.Errorf("after duplicate: saga status = %s, want completed", final.Status)
	}
}

// ---- Status reason cleanup test ----

// TestPurchaseFlow_InsufficientFunds_CleanStatusReason verifies that the status
// reason for insufficient-funds does not contain the duplicated wording
// "insufficient funds: insufficient funds".
func TestPurchaseFlow_InsufficientFunds_CleanStatusReason(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &recordingPaymentsClient{}
	handler := NewConsumerHandler(repo, payments, pub, discardLogger())
	ctx := context.Background()

	txnID := "txn-purchase-clean-reason"
	step := "purchase_debit"
	createPurchaseSaga(t, repo, txnID, StatusRunning, &step)

	env := newTestEnvelope(t, messaging.RoutingKeyWalletDebitRejected, txnID, messaging.WalletDebitRejected{
		TransactionID: txnID,
		UserID:        "user-1",
		Amount:        5000,
		Reason:        "insufficient funds",
		SourceStep:    "purchase_debit",
	})
	if err := handler.HandleOutcome(ctx, env); err != nil {
		t.Fatalf("handleWalletDebitRejected: %v", err)
	}

	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(updates))
	}
	if updates[0].Reason == nil {
		t.Fatal("expected status reason, got nil")
	}

	reason := *updates[0].Reason
	expected := "wallet debit rejected: insufficient funds"
	if reason != expected {
		t.Errorf("status_reason = %q, want %q", reason, expected)
	}

	// Explicitly verify the old duplicated wording is not present.
	if reason == "insufficient funds: insufficient funds" {
		t.Error("status_reason still contains the duplicated wording")
	}
}
