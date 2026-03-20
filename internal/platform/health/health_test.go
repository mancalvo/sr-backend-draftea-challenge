package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
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

	var status Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
	if status.Status != "ok" {
		t.Errorf("status.status = %q, want %q", status.Status, "ok")
	}
	if status.Service != "test-service" {
		t.Errorf("status.service = %q, want %q", status.Service, "test-service")
	}
}

// --- checker test helpers ---

type okChecker struct{ name string }

func (c *okChecker) Name() string                  { return c.name }
func (c *okChecker) Check(_ context.Context) error { return nil }

type failChecker struct {
	name string
	err  error
}

func (c *failChecker) Name() string                  { return c.name }
func (c *failChecker) Check(_ context.Context) error { return c.err }

func TestHandler_WithCheckers_AllOK(t *testing.T) {
	handler := Handler("svc",
		&okChecker{name: "postgres"},
		&okChecker{name: "rabbitmq"},
	)

	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var status Status
	decodeStatus(t, w, &status)

	if status.Status != "ok" {
		t.Errorf("status = %q, want %q", status.Status, "ok")
	}
	if status.Checks["postgres"] != "ok" {
		t.Errorf("checks.postgres = %q, want %q", status.Checks["postgres"], "ok")
	}
	if status.Checks["rabbitmq"] != "ok" {
		t.Errorf("checks.rabbitmq = %q, want %q", status.Checks["rabbitmq"], "ok")
	}
}

func TestHandler_WithCheckers_OneFails(t *testing.T) {
	handler := Handler("svc",
		&okChecker{name: "postgres"},
		&failChecker{name: "rabbitmq", err: fmt.Errorf("connection closed")},
	)

	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var status Status
	decodeStatus(t, w, &status)

	if status.Status != "degraded" {
		t.Errorf("status = %q, want %q", status.Status, "degraded")
	}
	if status.Checks["postgres"] != "ok" {
		t.Errorf("checks.postgres = %q, want %q", status.Checks["postgres"], "ok")
	}
	if status.Checks["rabbitmq"] != "connection closed" {
		t.Errorf("checks.rabbitmq = %q, want %q", status.Checks["rabbitmq"], "connection closed")
	}
}

func TestHandler_NoCheckers_OmitsChecks(t *testing.T) {
	handler := Handler("svc")

	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	var status Status
	decodeStatus(t, w, &status)

	if status.Checks != nil {
		t.Errorf("checks should be nil when no checkers, got %v", status.Checks)
	}
}

// --- DBPinger / RabbitMQChecker tests ---

type mockPinger struct{ err error }

func (m *mockPinger) PingContext(_ context.Context) error { return m.err }

func TestDBPinger_Check_OK(t *testing.T) {
	c := &DBPinger{Pinger: &mockPinger{}}
	if c.Name() != "postgres" {
		t.Errorf("name = %q, want postgres", c.Name())
	}
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDBPinger_Check_Error(t *testing.T) {
	c := &DBPinger{Pinger: &mockPinger{err: fmt.Errorf("db down")}}
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

type mockConn struct{ closed bool }

func (m *mockConn) IsClosed() bool { return m.closed }

func TestRabbitMQChecker_Check_OK(t *testing.T) {
	c := &RabbitMQChecker{Conn: &mockConn{closed: false}}
	if c.Name() != "rabbitmq" {
		t.Errorf("name = %q, want rabbitmq", c.Name())
	}
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRabbitMQChecker_Check_Closed(t *testing.T) {
	c := &RabbitMQChecker{Conn: &mockConn{closed: true}}
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("expected error for closed connection, got nil")
	}
}

// decodeStatus is a helper to extract the Status from the response body.
func decodeStatus(t *testing.T, w *httptest.ResponseRecorder, out *Status) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
}
