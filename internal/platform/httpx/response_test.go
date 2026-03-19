package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSON_Success(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"id": "123"}

	JSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Error("success = false, want true")
	}
	if resp.Error != nil {
		t.Error("error should be nil for success response")
	}
}

func TestJSON_Created(t *testing.T) {
	w := httptest.NewRecorder()

	JSON(w, http.StatusCreated, map[string]string{"status": "ok"})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Error("success = false, want true for 201")
	}
}

func TestError_BadRequest(t *testing.T) {
	w := httptest.NewRecorder()

	Error(w, http.StatusBadRequest, "invalid input")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("success = true, want false")
	}
	if resp.Error == nil {
		t.Fatal("error should not be nil")
	}
	if resp.Error.Message != "invalid input" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "invalid input")
	}
	if resp.Error.Code != "" {
		t.Errorf("error.code = %q, want empty", resp.Error.Code)
	}
}

func TestErrorWithCode(t *testing.T) {
	w := httptest.NewRecorder()

	ErrorWithCode(w, http.StatusConflict, "duplicate", "DUPLICATE_ENTRY")

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("success = true, want false")
	}
	if resp.Error == nil {
		t.Fatal("error should not be nil")
	}
	if resp.Error.Message != "duplicate" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "duplicate")
	}
	if resp.Error.Code != "DUPLICATE_ENTRY" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "DUPLICATE_ENTRY")
	}
}

func TestError_InternalServerError(t *testing.T) {
	w := httptest.NewRecorder()

	Error(w, http.StatusInternalServerError, "something broke")

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Success {
		t.Error("success = true, want false for 500")
	}
}
