package catalogaccess

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedRepo creates a MemoryRepository with one user, one offering, and optionally
// one active access record.
func seedRepo(withAccess bool) *MemoryRepository {
	repo := NewMemoryRepository()
	repo.Users["user-1"] = &User{
		ID:    "user-1",
		Email: "test@example.com",
		Name:  "Test User",
	}
	repo.Offerings["offering-1"] = &Offering{
		ID:       "offering-1",
		Name:     "Premium Plan",
		Price:    10000,
		Currency: "ARS",
		Active:   true,
	}
	repo.Offerings["offering-inactive"] = &Offering{
		ID:       "offering-inactive",
		Name:     "Deprecated Plan",
		Price:    5000,
		Currency: "ARS",
		Active:   false,
	}
	if withAccess {
		now := time.Now().UTC()
		repo.AccessRecords = append(repo.AccessRecords, &AccessRecord{
			ID:            "ar-seed",
			UserID:        "user-1",
			OfferingID:    "offering-1",
			TransactionID: "txn-original",
			Status:        AccessStatusActive,
			GrantedAt:     now,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	return repo
}

// newTestRouter creates a chi router wired to the handler under test.
func newTestRouter(handler *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestLogger(testLogger()))
	r.Get("/users/{user_id}/entitlements", handler.GetEntitlements)
	r.Post("/internal/purchase-precheck", handler.PurchasePrecheck)
	r.Post("/internal/refund-precheck", handler.RefundPrecheck)
	return r
}

// --- GET /users/{user_id}/entitlements ---

func TestGetEntitlements_UserNotFound(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/users/unknown-user/entitlements", nil)
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

func TestGetEntitlements_EmptyList(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/users/user-1/entitlements", nil)
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
	ents, ok := data["entitlements"].([]any)
	if !ok {
		t.Fatalf("expected entitlements to be an array, got %T", data["entitlements"])
	}
	if len(ents) != 0 {
		t.Errorf("expected 0 entitlements, got %d", len(ents))
	}
}

func TestGetEntitlements_WithActiveAccess(t *testing.T) {
	repo := seedRepo(true)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/users/user-1/entitlements", nil)
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

	data := resp.Data.(map[string]any)
	ents := data["entitlements"].([]any)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entitlement, got %d", len(ents))
	}

	ent := ents[0].(map[string]any)
	if ent["offering_id"] != "offering-1" {
		t.Errorf("offering_id = %v, want offering-1", ent["offering_id"])
	}
	if ent["offering_name"] != "Premium Plan" {
		t.Errorf("offering_name = %v, want Premium Plan", ent["offering_name"])
	}
}

// --- POST /internal/purchase-precheck ---

func TestPurchasePrecheck_Allowed(t *testing.T) {
	repo := seedRepo(false) // no existing access
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchasePrecheckRequest{
		UserID:     "user-1",
		OfferingID: "offering-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/purchase-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	if data["allowed"] != true {
		t.Errorf("allowed = %v, want true", data["allowed"])
	}
}

func TestPurchasePrecheck_UserNotFound(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchasePrecheckRequest{
		UserID:     "nonexistent",
		OfferingID: "offering-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/purchase-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != false {
		t.Errorf("allowed = %v, want false", data["allowed"])
	}
	if data["reason"] != "user not found" {
		t.Errorf("reason = %v, want 'user not found'", data["reason"])
	}
}

func TestPurchasePrecheck_OfferingNotFound(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchasePrecheckRequest{
		UserID:     "user-1",
		OfferingID: "nonexistent",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/purchase-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != false {
		t.Errorf("allowed = %v, want false", data["allowed"])
	}
	if data["reason"] != "offering not found" {
		t.Errorf("reason = %v, want 'offering not found'", data["reason"])
	}
}

func TestPurchasePrecheck_OfferingInactive(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchasePrecheckRequest{
		UserID:     "user-1",
		OfferingID: "offering-inactive",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/purchase-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != false {
		t.Errorf("allowed = %v, want false", data["allowed"])
	}
	if data["reason"] != "offering is not active" {
		t.Errorf("reason = %v, want 'offering is not active'", data["reason"])
	}
}

func TestPurchasePrecheck_DuplicateAccess(t *testing.T) {
	repo := seedRepo(true) // user already has active access
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchasePrecheckRequest{
		UserID:     "user-1",
		OfferingID: "offering-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/purchase-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != false {
		t.Errorf("allowed = %v, want false", data["allowed"])
	}
	if data["reason"] != "user already has active access to this offering" {
		t.Errorf("reason = %v, want 'user already has active access to this offering'", data["reason"])
	}
}

func TestPurchasePrecheck_MissingFields(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(PurchasePrecheckRequest{
		UserID: "user-1",
		// OfferingID missing
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/purchase-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- POST /internal/refund-precheck ---

func TestRefundPrecheck_Allowed(t *testing.T) {
	repo := seedRepo(true) // has active access linked to txn-original
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(RefundPrecheckRequest{
		UserID:        "user-1",
		OfferingID:    "offering-1",
		TransactionID: "txn-original",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/refund-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != true {
		t.Errorf("allowed = %v, want true", data["allowed"])
	}
}

func TestRefundPrecheck_NoActiveAccess(t *testing.T) {
	repo := seedRepo(false) // no access records
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(RefundPrecheckRequest{
		UserID:        "user-1",
		OfferingID:    "offering-1",
		TransactionID: "txn-nonexistent",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/refund-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != false {
		t.Errorf("allowed = %v, want false", data["allowed"])
	}
	if data["reason"] != "no active access found for this transaction" {
		t.Errorf("reason = %v, want 'no active access found for this transaction'", data["reason"])
	}
}

func TestRefundPrecheck_UserMismatch(t *testing.T) {
	repo := seedRepo(true) // access is for user-1
	// Add a second user
	repo.Users["user-2"] = &User{ID: "user-2", Email: "other@example.com", Name: "Other"}
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(RefundPrecheckRequest{
		UserID:        "user-2", // different user than access owner
		OfferingID:    "offering-1",
		TransactionID: "txn-original",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/refund-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var resp httpx.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	data := resp.Data.(map[string]any)
	if data["allowed"] != false {
		t.Errorf("allowed = %v, want false", data["allowed"])
	}
}

func TestRefundPrecheck_MissingFields(t *testing.T) {
	repo := seedRepo(false)
	h := NewHandler(repo, testLogger())
	router := newTestRouter(h)

	body, _ := json.Marshal(RefundPrecheckRequest{
		UserID:     "user-1",
		OfferingID: "offering-1",
		// TransactionID missing
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/refund-precheck", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
