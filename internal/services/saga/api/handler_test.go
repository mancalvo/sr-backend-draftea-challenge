package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

type (
	CatalogClient               = client.CatalogClient
	IdempotencyKey              = domain.IdempotencyKey
	MemoryRepository            = repository.MemoryRepository
	PaymentsClient              = client.PaymentsClient
	PrecheckResult              = client.PrecheckResult
	RegisterTransactionRequest  = client.RegisterTransactionRequest
	RegisterTransactionResponse = client.RegisterTransactionResponse
	Repository                  = repository.Repository
	SagaInstance                = domain.SagaInstance
	SagaOutcome                 = domain.SagaOutcome
	SagaStatus                  = domain.SagaStatus
	TransactionDetails          = client.TransactionDetails
)

const (
	SagaTypeDeposit = domain.SagaTypeDeposit
	SagaTypeRefund  = domain.SagaTypeRefund

	StatusRunning = domain.StatusRunning
)

type (
	DepositPayload  = workflows.DepositPayload
	PurchasePayload = workflows.PurchasePayload
	RefundPayload   = workflows.RefundPayload
)

var NewMemoryRepository = repository.NewMemoryRepository

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- Mock clients ----

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
	return &RegisterTransactionResponse{
		ID:     req.ID,
		Status: "pending",
	}, nil
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

type mockPublisher struct{}

func (m *mockPublisher) Publish(_ context.Context, _, _, _ string, _ any) error {
	return nil
}

type publishedMessage struct {
	Exchange      string
	RoutingKey    string
	CorrelationID string
	Payload       any
}

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

type noTransitionRepo struct {
	*MemoryRepository
	updateCalls int
	created     []*SagaInstance
}

func newNoTransitionRepo() *noTransitionRepo {
	return &noTransitionRepo{MemoryRepository: NewMemoryRepository()}
}

func (r *noTransitionRepo) CreateSaga(ctx context.Context, saga *SagaInstance) (*SagaInstance, error) {
	snapshot := *saga
	if saga.CurrentStep != nil {
		step := *saga.CurrentStep
		snapshot.CurrentStep = &step
	}
	if saga.Payload != nil {
		snapshot.Payload = append(json.RawMessage(nil), saga.Payload...)
	}
	r.created = append(r.created, &snapshot)
	return r.MemoryRepository.CreateSaga(ctx, saga)
}

func (r *noTransitionRepo) UpdateSagaStatus(_ context.Context, _ string, _ SagaStatus, _ *SagaOutcome, _ *string) (*SagaInstance, error) {
	r.updateCalls++
	return nil, errors.New("unexpected update saga status")
}

// newTestRouter wires up the saga handler routes on a chi router for testing.
func newTestRouter(handler *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestLogger(testLogger()))
	r.Post("/deposits", handler.HandleDeposit)
	r.Post("/purchases", handler.HandlePurchase)
	r.Post("/refunds", handler.HandleRefund)
	return r
}

func newTestHandler(repo Repository, catalog CatalogClient, payments PaymentsClient) *Handler {
	return NewHandler(repo, catalog, payments, &mockPublisher{}, 30*time.Second, testLogger())
}

// ---- POST /deposits ----

func TestHandleDeposit_Success(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "dep-key-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	var resp httpx.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	data := resp.Data.(map[string]any)
	if data["transaction_id"] == nil || data["transaction_id"] == "" {
		t.Error("expected non-empty transaction_id")
	}
	if data["status"] != "pending" {
		t.Errorf("status = %v, want pending", data["status"])
	}
}

func TestHandleDeposit_MissingFields(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID: "user-1",
		// Missing amount and idempotency_key
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR code, got %+v", resp.Error)
	}
}

func TestHandleDeposit_PaymentsUnavailable_FailFast(t *testing.T) {
	repo := NewMemoryRepository()
	payments := &mockPaymentsClient{
		registerErr: errors.New("connection refused"),
	}
	handler := newTestHandler(repo, &mockCatalogClient{}, payments)
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		IdempotencyKey: "dep-key-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestHandleDeposit_IdempotentAcceptance(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "dep-key-idem",
	})

	// First request.
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("first: status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	firstBody := rec.Body.String()
	var firstResp httpx.Response
	json.NewDecoder(bytes.NewReader([]byte(firstBody))).Decode(&firstResp)
	firstData := firstResp.Data.(map[string]any)
	firstTxnID := firstData["transaction_id"].(string)

	// Second request with same idempotency key.
	body, _ = json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "dep-key-idem",
	})
	req = httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("second: status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	secondBody := rec.Body.String()
	var secondResp httpx.Response
	json.NewDecoder(bytes.NewReader([]byte(secondBody))).Decode(&secondResp)
	secondData := secondResp.Data.(map[string]any)
	secondTxnID := secondData["transaction_id"].(string)

	// Both responses must return the same transaction_id.
	if firstTxnID != secondTxnID {
		t.Errorf("idempotent responses differ: first=%s, second=%s", firstTxnID, secondTxnID)
	}
	if firstBody != secondBody {
		t.Errorf("idempotent raw responses differ: first=%s, second=%s", firstBody, secondBody)
	}
}

// ---- POST /purchases ----

func TestHandlePurchase_Success(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-key-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
}

func TestHandlePurchase_PrecheckDenied_FailFast(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: false, Reason: "user already has active access"},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-key-2",
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "PRECHECK_DENIED" {
		t.Errorf("expected PRECHECK_DENIED error code, got %+v", resp.Error)
	}
}

func TestHandlePurchase_CatalogUnavailable_FailFast(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseErr: errors.New("connection refused"),
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-key-3",
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestHandlePurchase_PaymentsUnavailable_FailFast(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	payments := &mockPaymentsClient{
		registerErr: errors.New("connection refused"),
	}
	handler := newTestHandler(repo, catalog, payments)
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-key-4",
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestHandlePurchase_IdempotentAcceptance(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-key-idem",
	})

	// First request.
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("first: status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var firstResp httpx.Response
	json.NewDecoder(rec.Body).Decode(&firstResp)
	firstData := firstResp.Data.(map[string]any)
	firstTxnID := firstData["transaction_id"].(string)

	// Second request with same idempotency key.
	body, _ = json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-key-idem",
	})
	req = httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("second: status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var secondResp httpx.Response
	json.NewDecoder(rec.Body).Decode(&secondResp)
	secondData := secondResp.Data.(map[string]any)
	secondTxnID := secondData["transaction_id"].(string)

	if firstTxnID != secondTxnID {
		t.Errorf("idempotent responses differ: first=%s, second=%s", firstTxnID, secondTxnID)
	}
}

// ---- POST /refunds ----

func TestHandleRefund_Success(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		refundResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(RefundCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		TransactionID:  "orig-txn-1",
		IdempotencyKey: "ref-key-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
}

func TestHandleRefund_PrecheckDenied(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		refundResult: &PrecheckResult{Allowed: false, Reason: "no active access found"},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(RefundCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		TransactionID:  "orig-txn-1",
		IdempotencyKey: "ref-key-2",
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestHandleRefund_CatalogUnavailable_FailFast(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		refundErr: errors.New("timeout"),
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(RefundCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		TransactionID:  "orig-txn-1",
		IdempotencyKey: "ref-key-3",
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestHandleRefund_MissingFields(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(RefundCommand{
		UserID:     "user-1",
		OfferingID: "offering-1",
		// Missing TransactionID and IdempotencyKey
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR code, got %+v", resp.Error)
	}
}

// ---- POST /refunds workflow initiation ----

// TestHandleRefund_InitiatesWorkflow verifies that HandleRefund transitions the
// saga to running and publishes access.revoke.requested.
func TestHandleRefund_InitiatesWorkflow(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	catalog := &mockCatalogClient{
		refundResult: &PrecheckResult{Allowed: true},
	}
	payments := &mockPaymentsClient{}
	h := NewHandler(repo, catalog, payments, pub, 30*time.Second, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(RefundCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		TransactionID:  "orig-purchase-txn",
		IdempotencyKey: "ref-init-1",
	})

	req := httptest.NewRequest("POST", "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

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
	if s.CurrentStep == nil || *s.CurrentStep != "refund_revoke_access" {
		t.Errorf("current_step = %v, want refund_revoke_access", s.CurrentStep)
	}
	if s.Type != SagaTypeRefund {
		t.Errorf("saga type = %s, want refund", s.Type)
	}

	// Verify access.revoke.requested was published.
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyAccessRevokeRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyAccessRevokeRequested)
	}
	if msgs[0].Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", msgs[0].Exchange, messaging.ExchangeCommands)
	}

	revokePayload, ok := msgs[0].Payload.(messaging.AccessRevokeRequested)
	if !ok {
		t.Fatalf("expected AccessRevokeRequested payload, got %T", msgs[0].Payload)
	}
	// The TransactionID in the revoke request should be the original purchase txn.
	if revokePayload.TransactionID != "orig-purchase-txn" {
		t.Errorf("revoke transaction_id = %s, want orig-purchase-txn", revokePayload.TransactionID)
	}
	if revokePayload.UserID != "user-1" {
		t.Errorf("revoke user_id = %s, want user-1", revokePayload.UserID)
	}
	if revokePayload.OfferingID != "offering-1" {
		t.Errorf("revoke offering_id = %s, want offering-1", revokePayload.OfferingID)
	}
	// The correlationID should be the refund transaction ID.
	if msgs[0].CorrelationID != txnID {
		t.Errorf("correlation_id = %s, want %s", msgs[0].CorrelationID, txnID)
	}

	// Verify refund payload stored correctly.
	var refPayload RefundPayload
	if err := json.Unmarshal(s.Payload, &refPayload); err != nil {
		t.Fatalf("unmarshal refund payload: %v", err)
	}
	if refPayload.OriginalTransaction != "orig-purchase-txn" {
		t.Errorf("original_transaction = %s, want orig-purchase-txn", refPayload.OriginalTransaction)
	}
	if refPayload.Amount != 5000 {
		t.Errorf("amount = %d, want 5000", refPayload.Amount)
	}
}

// ---- Saga creation validation ----

func TestDepositCreatedSagaHasCorrectType(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "dep-saga-type",
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txnID := data["transaction_id"].(string)

	saga, err := repo.GetSagaByTransactionID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if saga.Type != SagaTypeDeposit {
		t.Errorf("saga type = %s, want deposit", saga.Type)
	}
	if saga.Status != StatusRunning {
		t.Errorf("saga status = %s, want running", saga.Status)
	}
	if saga.TimeoutAt == nil {
		t.Error("expected saga timeout_at to be set")
	}
}

// TestHandleDeposit_InitiatesWorkflow verifies that HandleDeposit transitions the
// saga to running and publishes payments.deposit.requested.
func TestHandleDeposit_InitiatesWorkflow(t *testing.T) {
	repo := NewMemoryRepository()
	pub := &recordingPublisher{}
	payments := &mockPaymentsClient{}
	h := NewHandler(repo, &mockCatalogClient{}, payments, pub, 30*time.Second, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "dep-init-1",
	})

	req := httptest.NewRequest("POST", "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

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
	if s.CurrentStep == nil || *s.CurrentStep != "deposit_charge" {
		t.Errorf("current_step = %v, want deposit_charge", s.CurrentStep)
	}
	if s.Type != SagaTypeDeposit {
		t.Errorf("saga type = %s, want deposit", s.Type)
	}

	// Verify payments.deposit.requested was published.
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	if msgs[0].RoutingKey != messaging.RoutingKeyDepositRequested {
		t.Errorf("routing key = %s, want %s", msgs[0].RoutingKey, messaging.RoutingKeyDepositRequested)
	}
	if msgs[0].Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %s, want %s", msgs[0].Exchange, messaging.ExchangeCommands)
	}

	depositPayload, ok := msgs[0].Payload.(messaging.DepositRequested)
	if !ok {
		t.Fatalf("expected DepositRequested payload, got %T", msgs[0].Payload)
	}
	if depositPayload.TransactionID != txnID {
		t.Errorf("deposit transaction_id = %s, want %s", depositPayload.TransactionID, txnID)
	}
	if depositPayload.UserID != "user-1" {
		t.Errorf("deposit user_id = %s, want user-1", depositPayload.UserID)
	}
	if depositPayload.Amount != 10000 {
		t.Errorf("deposit amount = %d, want 10000", depositPayload.Amount)
	}
	if depositPayload.Currency != "ARS" {
		t.Errorf("deposit currency = %s, want ARS", depositPayload.Currency)
	}
}

func TestPurchaseCreatedSagaHasCorrectPayload(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-payload",
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txnID := data["transaction_id"].(string)

	saga, err := repo.GetSagaByTransactionID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}

	var payload PurchasePayload
	if err := json.Unmarshal(saga.Payload, &payload); err != nil {
		t.Fatalf("unmarshal saga payload: %v", err)
	}
	if payload.UserID != "user-1" {
		t.Errorf("payload user_id = %s, want user-1", payload.UserID)
	}
	if payload.OfferingID != "offering-1" {
		t.Errorf("payload offering_id = %s, want offering-1", payload.OfferingID)
	}
	if payload.Amount != 5000 {
		t.Errorf("payload amount = %d, want 5000", payload.Amount)
	}
}

// ---- Idempotency mismatch tests ----

func TestHandleDeposit_IdempotencyMismatch_ReturnsConflict(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	// First request.
	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "dep-mismatch-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("first: status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	// Second request: same key, different amount.
	body, _ = json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         500,
		Currency:       "ARS",
		IdempotencyKey: "dep-mismatch-1",
	})
	req = httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("mismatch: status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "IDEMPOTENCY_MISMATCH" {
		t.Errorf("expected IDEMPOTENCY_MISMATCH code, got %+v", resp.Error)
	}
}

func TestHandlePurchase_IdempotencyMismatch_ReturnsConflict(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	// First request.
	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-mismatch-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("first: status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	// Second request: same key, different offering.
	body, _ = json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-2",
		IdempotencyKey: "pur-mismatch-1",
	})
	req = httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("mismatch: status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandlePurchase_RejectsClientMoneyFields(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body := []byte(`{"user_id":"user-1","offering_id":"offering-1","amount":999,"currency":"USD","idempotency_key":"pur-client-money"}`)
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandlePurchase_UsesAuthoritativeCatalogPricing(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true, Price: 7200, Currency: "USD"},
	}
	payments := &mockPaymentsClient{}
	pub := &recordingPublisher{}
	handler := NewHandler(repo, catalog, payments, pub, 30*time.Second, testLogger())
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "pur-mismatch-1",
	})

	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(payments.registerCalls) != 1 {
		t.Fatalf("register calls = %d, want 1", len(payments.registerCalls))
	}
	if payments.registerCalls[0].Amount != 7200 {
		t.Errorf("registered amount = %d, want 7200", payments.registerCalls[0].Amount)
	}
	if payments.registerCalls[0].Currency != "USD" {
		t.Errorf("registered currency = %s, want USD", payments.registerCalls[0].Currency)
	}

	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("published messages = %d, want 1", len(msgs))
	}
	payload, ok := msgs[0].Payload.(messaging.WalletDebitRequested)
	if !ok {
		t.Fatalf("payload type = %T, want WalletDebitRequested", msgs[0].Payload)
	}
	if payload.Amount != 7200 {
		t.Errorf("wallet debit amount = %d, want 7200", payload.Amount)
	}
	if payload.Currency != "USD" {
		t.Errorf("wallet debit currency = %s, want USD", payload.Currency)
	}
}

func TestCommandIngress_CreatesRunningSagaWithoutFollowupTransition(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		body        []byte
		currentStep string
		catalog     CatalogClient
		payments    PaymentsClient
	}{
		{
			name:        "deposit",
			path:        "/deposits",
			body:        mustJSON(t, DepositCommand{UserID: "user-1", Amount: 10000, Currency: "ARS", IdempotencyKey: "dep-running-create"}),
			currentStep: "deposit_charge",
			catalog:     &mockCatalogClient{},
			payments:    &mockPaymentsClient{},
		},
		{
			name:        "purchase",
			path:        "/purchases",
			body:        mustJSON(t, PurchaseCommand{UserID: "user-1", OfferingID: "offering-1", IdempotencyKey: "pur-running-create"}),
			currentStep: "purchase_debit",
			catalog:     &mockCatalogClient{purchaseResult: &PrecheckResult{Allowed: true}},
			payments:    &mockPaymentsClient{},
		},
		{
			name:        "refund",
			path:        "/refunds",
			body:        mustJSON(t, RefundCommand{UserID: "user-1", OfferingID: "offering-1", TransactionID: "orig-txn-1", IdempotencyKey: "ref-running-create"}),
			currentStep: "refund_revoke_access",
			catalog:     &mockCatalogClient{refundResult: &PrecheckResult{Allowed: true}},
			payments:    &mockPaymentsClient{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newNoTransitionRepo()
			handler := NewHandler(repo, tt.catalog, tt.payments, &mockPublisher{}, 30*time.Second, testLogger())
			router := newTestRouter(handler)

			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
			}
			if repo.updateCalls != 0 {
				t.Fatalf("unexpected update calls = %d", repo.updateCalls)
			}
			if len(repo.created) != 1 {
				t.Fatalf("created sagas = %d, want 1", len(repo.created))
			}
			if repo.created[0].Status != StatusRunning {
				t.Fatalf("created status = %s, want running", repo.created[0].Status)
			}
			if repo.created[0].CurrentStep == nil || *repo.created[0].CurrentStep != tt.currentStep {
				t.Fatalf("current_step = %v, want %s", repo.created[0].CurrentStep, tt.currentStep)
			}
		})
	}
}

func TestHandleRefund_RejectsClientMoneyFields(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		refundResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body := []byte(`{"user_id":"user-1","offering_id":"offering-1","transaction_id":"orig-txn-1","amount":999,"currency":"USD","idempotency_key":"ref-client-money"}`)
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleRefund_UsesOriginalTransactionMoney(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		refundResult: &PrecheckResult{Allowed: true},
	}
	offeringID := "offering-1"
	payments := &mockPaymentsClient{
		getResult: &TransactionDetails{
			UserID:     "user-1",
			Type:       "purchase",
			Status:     "completed",
			Amount:     8600,
			Currency:   "USD",
			OfferingID: &offeringID,
		},
	}
	pub := &recordingPublisher{}
	handler := NewHandler(repo, catalog, payments, pub, 30*time.Second, testLogger())
	router := newTestRouter(handler)

	body, _ := json.Marshal(RefundCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		TransactionID:  "orig-txn-1",
		IdempotencyKey: "ref-authoritative-money",
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(payments.registerCalls) != 1 {
		t.Fatalf("register calls = %d, want 1", len(payments.registerCalls))
	}
	if payments.registerCalls[0].Amount != 8600 {
		t.Errorf("registered amount = %d, want 8600", payments.registerCalls[0].Amount)
	}
	if payments.registerCalls[0].Currency != "USD" {
		t.Errorf("registered currency = %s, want USD", payments.registerCalls[0].Currency)
	}
	if payments.registerCalls[0].OriginalTransactionID == nil || *payments.registerCalls[0].OriginalTransactionID != "orig-txn-1" {
		t.Errorf("original_transaction_id = %v, want orig-txn-1", payments.registerCalls[0].OriginalTransactionID)
	}

	sagaResp := struct {
		Data struct {
			TransactionID string `json:"transaction_id"`
		} `json:"data"`
	}{}
	if err := json.NewDecoder(rec.Body).Decode(&sagaResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	s, err := repo.GetSagaByTransactionID(context.Background(), sagaResp.Data.TransactionID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}

	var payload RefundPayload
	if err := json.Unmarshal(s.Payload, &payload); err != nil {
		t.Fatalf("unmarshal refund payload: %v", err)
	}
	if payload.Amount != 8600 {
		t.Errorf("refund payload amount = %d, want 8600", payload.Amount)
	}
	if payload.Currency != "USD" {
		t.Errorf("refund payload currency = %s, want USD", payload.Currency)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return body
}

// TestIdempotencyKey_SameKeyDifferentScopes verifies that the same idempotency
// key used on different endpoints (scopes) does not collide.
func TestIdempotencyKey_SameKeyDifferentScopes(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
		refundResult:   &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	sharedKey := "shared-key-across-scopes"

	// Deposit with the shared key.
	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: sharedKey,
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("deposit: status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	// Purchase with the same key -> should NOT collide with deposit scope.
	body, _ = json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: sharedKey,
	})
	req = httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("purchase: status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
}

// ---- Validation error tests ----

func TestHandleDeposit_MalformedJSON_Returns400(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader([]byte(`{bad json`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleDeposit_MissingUserID_Returns422(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		Amount:         10000,
		IdempotencyKey: "dep-val-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR, got %+v", resp.Error)
	}
	// Verify the field-level detail.
	found := false
	for _, f := range resp.Error.Fields {
		if f.Field == "user_id" && f.Code == "required" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected field error for user_id/required, got %+v", resp.Error.Fields)
	}
}

func TestHandleDeposit_MissingIdempotencyKey_Returns422(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID: "user-1",
		Amount: 10000,
		// Missing idempotency_key
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	found := false
	for _, f := range resp.Error.Fields {
		if f.Field == "idempotency_key" && f.Code == "required" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected field error for idempotency_key/required, got %+v", resp.Error.Fields)
	}
}

func TestHandleDeposit_NonPositiveAmount_Returns422(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(DepositCommand{
		UserID:         "user-1",
		Amount:         -100,
		IdempotencyKey: "dep-neg-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/deposits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	found := false
	for _, f := range resp.Error.Fields {
		if f.Field == "amount" && f.Code == "must_be_positive" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected field error for amount/must_be_positive, got %+v", resp.Error.Fields)
	}
}

func TestHandlePurchase_MissingOfferingID_Returns422(t *testing.T) {
	repo := NewMemoryRepository()
	catalog := &mockCatalogClient{
		purchaseResult: &PrecheckResult{Allowed: true},
	}
	handler := newTestHandler(repo, catalog, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(PurchaseCommand{
		UserID:         "user-1",
		IdempotencyKey: "pur-val-1",
		// Missing offering_id
	})
	req := httptest.NewRequest(http.MethodPost, "/purchases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	found := false
	for _, f := range resp.Error.Fields {
		if f.Field == "offering_id" && f.Code == "required" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected field error for offering_id/required, got %+v", resp.Error.Fields)
	}
}

func TestHandleRefund_MissingTransactionID_Returns422(t *testing.T) {
	repo := NewMemoryRepository()
	handler := newTestHandler(repo, &mockCatalogClient{}, &mockPaymentsClient{})
	router := newTestRouter(handler)

	body, _ := json.Marshal(RefundCommand{
		UserID:         "user-1",
		OfferingID:     "offering-1",
		IdempotencyKey: "ref-val-1",
		// Missing transaction_id
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	found := false
	for _, f := range resp.Error.Fields {
		if f.Field == "transaction_id" && f.Code == "required" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected field error for transaction_id/required, got %+v", resp.Error.Fields)
	}
}
