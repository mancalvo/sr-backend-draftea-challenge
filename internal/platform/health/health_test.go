package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

func TestHandler_ReturnsOK(t *testing.T) {
	handler := Handler("test-service")

	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}

	var resp httpx.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Error("success = false, want true")
	}

	// Decode the data field
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("failed to marshal data: %v", err)
	}
	var status Status
	if err := json.Unmarshal(dataBytes, &status); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
	if status.Status != "ok" {
		t.Errorf("status.status = %q, want %q", status.Status, "ok")
	}
	if status.Service != "test-service" {
		t.Errorf("status.service = %q, want %q", status.Service, "test-service")
	}
}
