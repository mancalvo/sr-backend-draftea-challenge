package httpx

import (
	"net/http"
	"strings"
	"testing"
)

func TestDecode_ValidJSON(t *testing.T) {
	body := `{"name":"test","value":42}`
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var dst struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	if err := Decode(r, &dst); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if dst.Name != "test" {
		t.Errorf("Name = %q, want %q", dst.Name, "test")
	}
	if dst.Value != 42 {
		t.Errorf("Value = %d, want %d", dst.Value, 42)
	}
}

func TestDecode_InvalidJSON(t *testing.T) {
	body := `{invalid`
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var dst struct{ Name string }
	if err := Decode(r, &dst); err == nil {
		t.Error("Decode should return error for invalid JSON")
	}
}

func TestDecode_NilBody(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/", nil)
	r.Body = nil

	var dst struct{ Name string }
	if err := Decode(r, &dst); err == nil {
		t.Error("Decode should return error for nil body")
	}
}

func TestDecode_UnknownFields(t *testing.T) {
	body := `{"name":"test","unknown_field":"value"}`
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var dst struct {
		Name string `json:"name"`
	}
	if err := Decode(r, &dst); err == nil {
		t.Error("Decode should return error for unknown fields")
	}
}

func TestDecode_RejectsUnsupportedContentType(t *testing.T) {
	body := `{"name":"test"}`
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "text/plain")

	var dst struct {
		Name string `json:"name"`
	}
	err := Decode(r, &dst)
	if err == nil {
		t.Fatal("Decode should return error for unsupported content type")
	}

	var requestErr *RequestError
	if !AsRequestError(err, &requestErr) {
		t.Fatalf("expected RequestError, got %T", err)
	}
	if requestErr.StatusCode() != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", requestErr.StatusCode(), http.StatusUnsupportedMediaType)
	}
}

func TestDecode_RejectsMultipleJSONValues(t *testing.T) {
	body := `{"name":"test"}{"name":"extra"}`
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	var dst struct {
		Name string `json:"name"`
	}
	if err := Decode(r, &dst); err == nil {
		t.Fatal("Decode should return error for multiple JSON values")
	}
}

func TestDecode_RequestBodyTooLarge(t *testing.T) {
	body := `{"name":"` + strings.Repeat("a", maxDecodeBodyBytes) + `"}`
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	var dst struct {
		Name string `json:"name"`
	}

	err := Decode(r, &dst)
	if err == nil {
		t.Fatal("Decode should return error for oversized request body")
	}

	var requestErr *RequestError
	if !AsRequestError(err, &requestErr) {
		t.Fatalf("expected RequestError, got %T", err)
	}
	if requestErr.StatusCode() != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", requestErr.StatusCode(), http.StatusRequestEntityTooLarge)
	}
}
