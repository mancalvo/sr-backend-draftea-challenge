package saga

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

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
	return m.purchaseResult, m.purchaseErr
}

func (m *mockCatalogClient) RefundPrecheck(_ context.Context, _, _, _ string) (*PrecheckResult, error) {
	return m.refundResult, m.refundErr
}

type mockPaymentsClient struct {
	registerResult *RegisterTransactionResponse
	registerErr    error
	updateErr      error
}

func (m *mockPaymentsClient) RegisterTransaction(_ context.Context, req RegisterTransactionRequest) (*RegisterTransactionResponse, error) {
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

func (m *mockPaymentsClient) UpdateTransactionStatus(_ context.Context, _ string, _ string, _ *string) error {
	return m.updateErr
}

type mockPublisher struct{}

func (m *mockPublisher) Publish(_ context.Context, _, _, _ string, _ any) error {
	return nil
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

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
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

	var firstResp httpx.Response
	json.NewDecoder(rec.Body).Decode(&firstResp)
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

	var secondResp httpx.Response
	json.NewDecoder(rec.Body).Decode(&secondResp)
	secondData := secondResp.Data.(map[string]any)
	secondTxnID := secondData["transaction_id"].(string)

	// Both responses must return the same transaction_id.
	if firstTxnID != secondTxnID {
		t.Errorf("idempotent responses differ: first=%s, second=%s", firstTxnID, secondTxnID)
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
		Amount:         5000,
		Currency:       "ARS",
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
		Amount:         5000,
		Currency:       "ARS",
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
		Amount:         5000,
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
		Amount:         5000,
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
		Amount:         5000,
		Currency:       "ARS",
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
		Amount:         5000,
		Currency:       "ARS",
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
		Amount:         5000,
		Currency:       "ARS",
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
		Amount:         5000,
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
		Amount:         5000,
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
		// Missing TransactionID, Amount, IdempotencyKey
	})
	req := httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
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
		Amount:         5000,
		Currency:       "ARS",
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
		Amount:         5000,
		Currency:       "ARS",
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
