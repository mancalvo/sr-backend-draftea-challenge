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
