package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLogger_SetsCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	cid := w.Header().Get(HeaderCorrelationID)
	if cid == "" {
		t.Error("correlation ID should be set in response header")
	}
}

func TestRequestLogger_PreservesExistingCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set(HeaderCorrelationID, "existing-id-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	cid := w.Header().Get(HeaderCorrelationID)
	if cid != "existing-id-123" {
		t.Errorf("correlation ID = %q, want %q", cid, "existing-id-123")
	}
}

func TestRecoverer_CatchesPanic(t *testing.T) {
	handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	r := httptest.NewRequest(http.MethodGet, "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRecoverer_PassesThrough(t *testing.T) {
	handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`ok`))
	}))

	r := httptest.NewRequest(http.MethodGet, "/ok", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestResponseWriter_CapturesStatusCode(t *testing.T) {
	w := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)
	if rw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want %d", rw.statusCode, http.StatusNotFound)
	}
}
