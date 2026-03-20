package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestValidationError(t *testing.T) {
	w := httptest.NewRecorder()

	fields := []FieldError{
		{Field: "user_id", Code: "required"},
		{Field: "amount", Code: "must_be_positive"},
	}
	ValidationError(w, fields)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnprocessableEntity)
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
	if resp.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("error.code = %q, want VALIDATION_ERROR", resp.Error.Code)
	}
	if resp.Error.Message != "Request validation failed" {
		t.Errorf("error.message = %q, want 'Request validation failed'", resp.Error.Message)
	}
	if len(resp.Error.Fields) != 2 {
		t.Fatalf("expected 2 field errors, got %d", len(resp.Error.Fields))
	}
	if resp.Error.Fields[0].Field != "user_id" || resp.Error.Fields[0].Code != "required" {
		t.Errorf("field[0] = %+v, want user_id/required", resp.Error.Fields[0])
	}
	if resp.Error.Fields[1].Field != "amount" || resp.Error.Fields[1].Code != "must_be_positive" {
		t.Errorf("field[1] = %+v, want amount/must_be_positive", resp.Error.Fields[1])
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

func TestMarshalResponse_MatchesJSONWriter(t *testing.T) {
	body, err := MarshalResponse(http.StatusAccepted, map[string]string{"status": "accepted"})
	if err != nil {
		t.Fatalf("MarshalResponse: %v", err)
	}

	w := httptest.NewRecorder()
	JSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})

	if !bytes.Equal(body, w.Body.Bytes()) {
		t.Fatalf("marshal body = %s, want %s", string(body), w.Body.String())
	}
}

func TestWriteDecodeError_UsesRequestErrorStatus(t *testing.T) {
	w := httptest.NewRecorder()

	WriteDecodeError(w, &RequestError{
		status:  http.StatusUnsupportedMediaType,
		message: "Content-Type must be application/json",
	})

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
}

func TestAsRequestError_UnwrapsWrappedError(t *testing.T) {
	err := errors.New("outer")
	err = errors.Join(err, &RequestError{status: http.StatusBadRequest, message: "bad request"})

	var requestErr *RequestError
	if !AsRequestError(err, &requestErr) {
		t.Fatal("expected wrapped RequestError")
	}
	if requestErr.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", requestErr.StatusCode(), http.StatusBadRequest)
	}
}
