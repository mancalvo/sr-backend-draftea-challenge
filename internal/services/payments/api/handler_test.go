package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"
	paymentsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/repository"
	paymentsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/service"
)

var NewMemoryRepository = paymentsrepository.NewMemoryRepository

type (
	Transaction       = domain.Transaction
	TransactionType   = domain.TransactionType
	TransactionStatus = domain.TransactionStatus
)

const (
	TransactionTypeDeposit  = domain.TransactionTypeDeposit
	TransactionTypePurchase = domain.TransactionTypePurchase
	TransactionTypeRefund   = domain.TransactionTypeRefund

	StatusPending   = domain.StatusPending
	StatusCompleted = domain.StatusCompleted
	StatusFailed    = domain.StatusFailed
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestRouter wires up the payments handler routes on a chi router for testing.
func newTestRouter(handler *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestLogger(testLogger()))
	r.Post("/internal/transactions", handler.CreateTransaction)
	r.Patch("/internal/transactions/{transaction_id}/status", handler.UpdateTransactionStatus)
	r.Get("/transactions/{transaction_id}", handler.GetTransaction)
	r.Get("/transactions", handler.ListTransactions)
	return r
}

// --- POST /internal/transactions ---

func TestCreateTransaction_Success(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(CreateTransactionRequest{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeDeposit,
		Amount:   10000,
		Currency: "ARS",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp httpx.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}

	data := resp.Data.(map[string]any)
	if data["id"] != "txn-1" {
		t.Errorf("id = %v, want txn-1", data["id"])
	}
	if data["status"] != "pending" {
		t.Errorf("status = %v, want pending", data["status"])
	}
}

func TestCreateTransaction_WithOfferingID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	offeringID := "offering-1"
	body, _ := json.Marshal(CreateTransactionRequest{
		ID:         "txn-1",
		UserID:     "user-1",
		Type:       TransactionTypePurchase,
		Amount:     5000,
		Currency:   "ARS",
		OfferingID: &offeringID,
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["offering_id"] != "offering-1" {
		t.Errorf("offering_id = %v, want offering-1", data["offering_id"])
	}
}

func TestCreateTransaction_RefundRequiresOriginalTransactionID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(CreateTransactionRequest{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeRefund,
		Amount:   5000,
		Currency: "ARS",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateTransaction_RefundPersistsOriginalTransactionID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	originalTransactionID := "txn-purchase-1"
	body, _ := json.Marshal(CreateTransactionRequest{
		ID:                    "txn-1",
		UserID:                "user-1",
		Type:                  TransactionTypeRefund,
		Amount:                5000,
		Currency:              "ARS",
		OriginalTransactionID: &originalTransactionID,
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp httpx.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := resp.Data.(map[string]any)
	if data["original_transaction_id"] != originalTransactionID {
		t.Errorf("original_transaction_id = %v, want %s", data["original_transaction_id"], originalTransactionID)
	}
}

func TestCreateTransaction_DuplicateID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(CreateTransactionRequest{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeDeposit,
		Amount:   10000,
		Currency: "ARS",
	})

	// First create.
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, want %d", rec.Code, http.StatusCreated)
	}

	// Duplicate create.
	body, _ = json.Marshal(CreateTransactionRequest{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeDeposit,
		Amount:   10000,
		Currency: "ARS",
	})
	req = httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestCreateTransaction_MissingFields(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(CreateTransactionRequest{
		UserID: "user-1",
		// Missing ID, Amount, Type
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateTransaction_InvalidType(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(map[string]any{
		"id":       "txn-1",
		"user_id":  "user-1",
		"type":     "transfer",
		"amount":   10000,
		"currency": "ARS",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateTransaction_DefaultCurrency(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(map[string]any{
		"id":      "txn-1",
		"user_id": "user-1",
		"type":    "deposit",
		"amount":  10000,
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/transactions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["currency"] != "ARS" {
		t.Errorf("currency = %v, want ARS (default)", data["currency"])
	}
}

// --- PATCH /internal/transactions/{transaction_id}/status ---

func TestUpdateTransactionStatus_LegalTransition(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	// Seed a pending transaction.
	repo.CreateTransaction(context.Background(), &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	})

	body, _ := json.Marshal(UpdateStatusRequest{Status: StatusCompleted})
	req := httptest.NewRequest(http.MethodPatch, "/internal/transactions/txn-1/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["status"] != "completed" {
		t.Errorf("status = %v, want completed", data["status"])
	}
}

func TestUpdateTransactionStatus_IllegalTransition(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	repo.CreateTransaction(context.Background(), &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusCompleted, Amount: 1000, Currency: "ARS",
	})

	body, _ := json.Marshal(UpdateStatusRequest{Status: StatusPending})
	req := httptest.NewRequest(http.MethodPatch, "/internal/transactions/txn-1/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "ILLEGAL_TRANSITION" {
		t.Errorf("expected ILLEGAL_TRANSITION error code, got %+v", resp.Error)
	}
}

func TestUpdateTransactionStatus_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(UpdateStatusRequest{Status: StatusCompleted})
	req := httptest.NewRequest(http.MethodPatch, "/internal/transactions/nonexistent/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestUpdateTransactionStatus_InvalidStatus(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(map[string]any{"status": "unknown"})
	req := httptest.NewRequest(http.MethodPatch, "/internal/transactions/txn-1/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateTransactionStatus_WithReason(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	repo.CreateTransaction(context.Background(), &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	})

	reason := "provider timeout"
	body, _ := json.Marshal(UpdateStatusRequest{Status: StatusFailed, StatusReason: &reason})
	req := httptest.NewRequest(http.MethodPatch, "/internal/transactions/txn-1/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["status_reason"] != "provider timeout" {
		t.Errorf("status_reason = %v, want 'provider timeout'", data["status_reason"])
	}
}

func TestUpdateTransactionStatus_WithProviderReference(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	repo.CreateTransaction(context.Background(), &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	})

	providerReference := "sim-txn-1"
	body, _ := json.Marshal(UpdateStatusRequest{Status: StatusPending, ProviderReference: &providerReference})
	req := httptest.NewRequest(http.MethodPatch, "/internal/transactions/txn-1/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["provider_reference"] != providerReference {
		t.Errorf("provider_reference = %v, want %s", data["provider_reference"], providerReference)
	}
}

// --- GET /transactions/{transaction_id} ---

func TestGetTransaction_Found(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	offeringID := "offering-1"
	originalTransactionID := "txn-purchase-1"
	providerReference := "sim-txn-1"
	repo.CreateTransaction(context.Background(), &Transaction{
		ID:                    "txn-1",
		UserID:                "user-1",
		Type:                  TransactionTypeRefund,
		Status:                StatusPending,
		Amount:                10000,
		Currency:              "ARS",
		OfferingID:            &offeringID,
		OriginalTransactionID: &originalTransactionID,
		ProviderReference:     &providerReference,
	})

	req := httptest.NewRequest(http.MethodGet, "/transactions/txn-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Error("expected success=true")
	}
	data := resp.Data.(map[string]any)
	if data["id"] != "txn-1" {
		t.Errorf("id = %v, want txn-1", data["id"])
	}
	if data["offering_id"] != offeringID {
		t.Errorf("offering_id = %v, want %s", data["offering_id"], offeringID)
	}
	if data["original_transaction_id"] != originalTransactionID {
		t.Errorf("original_transaction_id = %v, want %s", data["original_transaction_id"], originalTransactionID)
	}
	if data["provider_reference"] != providerReference {
		t.Errorf("provider_reference = %v, want %s", data["provider_reference"], providerReference)
	}
}

func TestGetTransaction_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/transactions/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- GET /transactions?user_id={user_id} ---

func TestListTransactions_Empty(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txns := data["transactions"].([]any)
	if len(txns) != 0 {
		t.Errorf("expected 0 transactions, got %d", len(txns))
	}
	if data["has_more"] != false {
		t.Error("expected has_more=false")
	}
}

func TestListTransactions_MissingUserID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestListTransactions_ReturnsUserTransactionsOnly(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	ctx := context.Background()
	repo.CreateTransaction(ctx, &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	})
	repo.CreateTransaction(ctx, &Transaction{
		ID: "txn-2", UserID: "user-2", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 2000, Currency: "ARS",
	})

	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txns := data["transactions"].([]any)
	if len(txns) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txns))
	}
	txn := txns[0].(map[string]any)
	if txn["id"] != "txn-1" {
		t.Errorf("id = %v, want txn-1", txn["id"])
	}
}

// --- Pagination handler tests ---

func TestListTransactions_DefaultLimitApplied(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	ctx := context.Background()
	// Create 3 transactions (fewer than default limit).
	for i := 0; i < 3; i++ {
		time.Sleep(time.Millisecond)
		repo.CreateTransaction(ctx, &Transaction{
			ID: fmt.Sprintf("txn-%d", i), UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
		})
	}

	// Request without limit param — should use default and return all 3.
	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txns := data["transactions"].([]any)
	if len(txns) != 3 {
		t.Errorf("expected 3 transactions, got %d", len(txns))
	}
	if data["has_more"] != false {
		t.Error("expected has_more=false when all results fit in default limit")
	}
}

func TestListTransactions_ExplicitLimitRespected(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond)
		repo.CreateTransaction(ctx, &Transaction{
			ID: fmt.Sprintf("txn-%d", i), UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1&limit=2", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txns := data["transactions"].([]any)
	if len(txns) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(txns))
	}
	if data["has_more"] != true {
		t.Error("expected has_more=true")
	}
	if data["next_cursor"] == nil {
		t.Error("expected next_cursor to be set")
	}
}

func TestListTransactions_Page1ReturnsNextCursor(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond)
		repo.CreateTransaction(ctx, &Transaction{
			ID: fmt.Sprintf("txn-%d", i), UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
		})
	}

	// Page 1: limit=3.
	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1&limit=3", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["next_cursor"] == nil {
		t.Fatal("page 1: expected next_cursor")
	}

	// Page 2: use cursor from page 1.
	cursor := data["next_cursor"].(string)
	req2 := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1&limit=3&cursor="+cursor, nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("page 2: status = %d, want %d; body: %s", rec2.Code, http.StatusOK, rec2.Body.String())
	}

	var resp2 httpx.Response
	json.NewDecoder(rec2.Body).Decode(&resp2)
	data2 := resp2.Data.(map[string]any)
	txns2 := data2["transactions"].([]any)
	if len(txns2) != 2 {
		t.Errorf("page 2: expected 2 transactions, got %d", len(txns2))
	}

	// Verify no duplicates between pages.
	txns1 := data["transactions"].([]any)
	seen := make(map[string]bool)
	for _, raw := range txns1 {
		id := raw.(map[string]any)["id"].(string)
		seen[id] = true
	}
	for _, raw := range txns2 {
		id := raw.(map[string]any)["id"].(string)
		if seen[id] {
			t.Errorf("duplicate transaction across pages: %s", id)
		}
	}
}

func TestListTransactions_InvalidCursorReturnsError(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1&cursor=not-a-valid-cursor", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != "INVALID_CURSOR" {
		t.Errorf("expected INVALID_CURSOR error code, got %+v", resp.Error)
	}
}

func TestListTransactions_InvalidLimitReturnsError(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	tests := []struct {
		name  string
		limit string
	}{
		{"negative", "-1"},
		{"zero", "0"},
		{"non-numeric", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1&limit="+tt.limit, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}

			var resp httpx.Response
			json.NewDecoder(rec.Body).Decode(&resp)
			if resp.Error == nil || resp.Error.Code != "INVALID_LIMIT" {
				t.Errorf("expected INVALID_LIMIT error code, got %+v", resp.Error)
			}
		})
	}
}

func TestListTransactions_StableOrderWithSameCreatedAt(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(paymentsservice.New(repo), testLogger())
	router := newTestRouter(h)

	// Insert transactions with the same timestamp directly.
	now := time.Now().UTC()
	for _, id := range []string{"txn-c", "txn-a", "txn-b"} {
		txn := &Transaction{
			ID: id, UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
			CreatedAt: now, UpdatedAt: now,
		}
		repo.SeedTransaction(txn)
	}

	req := httptest.NewRequest(http.MethodGet, "/transactions?user_id=user-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	txns := data["transactions"].([]any)

	// Expect descending ID order: c, b, a.
	expected := []string{"txn-c", "txn-b", "txn-a"}
	for i, want := range expected {
		got := txns[i].(map[string]any)["id"].(string)
		if got != want {
			t.Errorf("position %d: got %s, want %s", i, got, want)
		}
	}
}
