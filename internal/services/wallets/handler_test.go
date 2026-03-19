package wallets

import (
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

// newTestRouter creates a chi router wired to the handler under test.
func newTestRouter(handler *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestLogger(testLogger()))
	r.Get("/wallets/{user_id}/balance", handler.GetBalance)
	return r
}

// --- GET /wallets/{user_id}/balance ---

func TestGetBalance_Success(t *testing.T) {
	repo := seedRepo(10000)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/wallets/user-1/balance", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be a map, got %T", resp.Data)
	}
	if data["user_id"] != "user-1" {
		t.Errorf("user_id = %v, want user-1", data["user_id"])
	}
	// JSON numbers are float64.
	if balance, ok := data["balance"].(float64); !ok || int64(balance) != 10000 {
		t.Errorf("balance = %v, want 10000", data["balance"])
	}
	if data["currency"] != "ARS" {
		t.Errorf("currency = %v, want ARS", data["currency"])
	}
}

func TestGetBalance_WalletNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/wallets/unknown-user/balance", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var resp httpx.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
}

func TestGetBalance_AfterDebit(t *testing.T) {
	repo := seedRepo(10000)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	// Perform a debit first.
	_, err := repo.Debit(nil, "user-1", "txn-1", "purchase_debit", 3000)
	if err != nil {
		t.Fatalf("debit: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wallets/user-1/balance", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	data := resp.Data.(map[string]any)
	if balance, ok := data["balance"].(float64); !ok || int64(balance) != 7000 {
		t.Errorf("balance = %v, want 7000", data["balance"])
	}
}
