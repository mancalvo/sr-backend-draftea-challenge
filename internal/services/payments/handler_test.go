package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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

func TestCreateTransaction_DuplicateID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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

// --- GET /transactions/{transaction_id} ---

func TestGetTransaction_Found(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	repo.CreateTransaction(context.Background(), &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 10000, Currency: "ARS",
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
}

func TestGetTransaction_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
}

func TestListTransactions_MissingUserID(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(repo, testLogger())
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
	h := NewHandler(repo, testLogger())
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
