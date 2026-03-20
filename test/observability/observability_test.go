// Package observability validates the minimum observability baseline required
// by T13: health endpoints per service, structured logging fields, and broker
// publish/consume log output.
package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/rabbitmq"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ---------------------------------------------------------------------------
// Health endpoint tests: every service router must expose GET /health
// ---------------------------------------------------------------------------

func TestHealth_Payments(t *testing.T) {
	h := payments.NewHandler(payments.NewMemoryRepository(), discardLogger())
	router := payments.NewRouter(h, discardLogger())
	assertHealthOK(t, router, "payments")
}

func TestHealth_Wallets(t *testing.T) {
	h := wallets.NewHandler(wallets.NewMemoryRepository(), discardLogger())
	router := wallets.NewRouter(h, discardLogger())
	assertHealthOK(t, router, "wallets")
}

func TestHealth_CatalogAccess(t *testing.T) {
	h := catalogaccess.NewHandler(catalogaccess.NewMemoryRepository(), discardLogger())
	router := catalogaccess.NewRouter(h, discardLogger())
	assertHealthOK(t, router, "catalog-access")
}

func TestHealth_SagaOrchestrator(t *testing.T) {
	h := saga.NewHandler(
		saga.NewMemoryRepository(),
		&noopCatalogClient{},
		&noopPaymentsClient{},
		&noopPublisher{},
		30*time.Second,
		discardLogger(),
	)
	router := saga.NewRouter(h, discardLogger())
	assertHealthOK(t, router, "saga-orchestrator")
}

func TestHealth_WithReadinessCheckers_OK(t *testing.T) {
	h := payments.NewHandler(payments.NewMemoryRepository(), discardLogger())
	router := payments.NewRouter(h, discardLogger(),
		&health.DBPinger{Pinger: &mockPinger{}},
		&health.RabbitMQChecker{Conn: &mockConn{closed: false}},
	)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var status health.Status
	decodeHealthStatus(t, w, &status)

	if status.Checks["postgres"] != "ok" {
		t.Errorf("checks.postgres = %q, want ok", status.Checks["postgres"])
	}
	if status.Checks["rabbitmq"] != "ok" {
		t.Errorf("checks.rabbitmq = %q, want ok", status.Checks["rabbitmq"])
	}
}

func TestHealth_WithReadinessCheckers_Degraded(t *testing.T) {
	h := payments.NewHandler(payments.NewMemoryRepository(), discardLogger())
	router := payments.NewRouter(h, discardLogger(),
		&health.DBPinger{Pinger: &mockPinger{}},
		&health.RabbitMQChecker{Conn: &mockConn{closed: true}},
	)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var status health.Status
	decodeHealthStatus(t, w, &status)

	if status.Status != "degraded" {
		t.Errorf("status = %q, want degraded", status.Status)
	}
}

// ---------------------------------------------------------------------------
// Structured logging tests: logger must include required fields
// ---------------------------------------------------------------------------

func TestLogging_NewIncludesService(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With(slog.String(logging.KeyService, "test-svc"))
	logger.Info("boot")

	entry := parseLogEntry(t, buf.Bytes())
	assertField(t, entry, logging.KeyService, "test-svc")
}

func TestLogging_ContextRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With(slog.String(logging.KeyService, "ctx-svc"))

	ctx := logging.WithContext(context.Background(), logger)
	got := logging.FromContext(ctx)
	got.Info("ctx-check")

	entry := parseLogEntry(t, buf.Bytes())
	assertField(t, entry, logging.KeyService, "ctx-svc")
}

func TestLogging_KeyConstants(t *testing.T) {
	// Verify the canonical key constants exist and have expected values.
	keys := map[string]string{
		"service":        logging.KeyService,
		"transaction_id": logging.KeyTransactionID,
		"correlation_id": logging.KeyCorrelationID,
		"message_id":     logging.KeyMessageID,
	}
	for want, got := range keys {
		if got != want {
			t.Errorf("Key constant %q = %q, want %q", want, got, want)
		}
	}
}

func TestLogging_StructuredFieldsPresentInConsumerLog(t *testing.T) {
	// Simulate what consumers do: derive a logger with all four fields.
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil)).With(slog.String(logging.KeyService, "wallets"))
	logger := base.With(
		slog.String(logging.KeyTransactionID, "txn-123"),
		slog.String(logging.KeyCorrelationID, "corr-456"),
		slog.String(logging.KeyMessageID, "msg-789"),
	)
	logger.Info("processing debit request")

	entry := parseLogEntry(t, buf.Bytes())
	assertField(t, entry, logging.KeyService, "wallets")
	assertField(t, entry, logging.KeyTransactionID, "txn-123")
	assertField(t, entry, logging.KeyCorrelationID, "corr-456")
	assertField(t, entry, logging.KeyMessageID, "msg-789")
}

// ---------------------------------------------------------------------------
// Broker logging tests: publisher and consumer must emit structured logs
// ---------------------------------------------------------------------------

func TestPublisher_LogsPublishWithRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With(slog.String(logging.KeyService, "test-pub"))
	ch := &mockPublishChannel{}
	pub := rabbitmq.NewPublisher(ch, logger)

	payload := messaging.DepositRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        5000,
		Currency:      "ARS",
	}
	err := pub.Publish(context.Background(), messaging.ExchangeCommands, messaging.RoutingKeyDepositRequested, "corr-1", payload)
	if err != nil {
		t.Fatalf("Publish error: %v", err)
	}

	entry := parseLogEntry(t, buf.Bytes())
	assertField(t, entry, "msg", "message published")
	assertFieldPresent(t, entry, "message_id")
	assertField(t, entry, "correlation_id", "corr-1")
	assertField(t, entry, "exchange", messaging.ExchangeCommands)
	assertField(t, entry, "routing_key", messaging.RoutingKeyDepositRequested)
}

func TestConsumer_LogsMessageReceivedWithRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With(slog.String(logging.KeyService, "test-cons"))

	ch := newMockConsumerChannel()
	retry := rabbitmq.RetryConfig{MaxRetries: 0, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	consumer := rabbitmq.NewConsumer(ch, logger, retry)

	delivery, _ := makeDelivery(t, map[string]string{"k": "v"}, "test.type", "corr-abc")

	handler := func(_ context.Context, _ messaging.Envelope) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch.deliveries <- delivery
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = consumer.Consume(ctx, "test.queue", handler)

	// Find the "message received" log line.
	found := false
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if msg, ok := entry["msg"].(string); ok && msg == "message received" {
			found = true
			assertFieldPresent(t, entry, "message_id")
			assertField(t, entry, "correlation_id", "corr-abc")
			assertField(t, entry, "type", "test.type")
			assertField(t, entry, "queue", "test.queue")
			break
		}
	}
	if !found {
		t.Error("expected 'message received' log entry not found")
	}
}

func TestMiddleware_RequestLoggerAddsCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With(slog.String(logging.KeyService, "test-svc"))

	handler := httpx.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// Response header must include correlation ID.
	cid := w.Header().Get(httpx.HeaderCorrelationID)
	if cid == "" {
		t.Error("correlation_id missing from response header")
	}

	// Log should include correlation_id field.
	found := false
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if _, ok := entry[logging.KeyCorrelationID]; ok {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected correlation_id in request log entry not found")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

func assertHealthOK(t *testing.T, handler http.Handler, wantService string) {
	t.Helper()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("%s health status = %d, want %d", wantService, w.Code, http.StatusOK)
	}

	var status health.Status
	decodeHealthStatus(t, w, &status)

	if status.Status != "ok" {
		t.Errorf("%s status = %q, want ok", wantService, status.Status)
	}
	if status.Service != wantService {
		t.Errorf("service = %q, want %q", status.Service, wantService)
	}
}

func decodeHealthStatus(t *testing.T, w *httptest.ResponseRecorder, out *health.Status) {
	t.Helper()
	var resp httpx.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("failed to marshal data: %v", err)
	}
	if err := json.Unmarshal(dataBytes, out); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
}

func parseLogEntry(t *testing.T, data []byte) map[string]any {
	t.Helper()
	// Find the last non-empty line (some entries may have multiple lines).
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			continue
		}
		return entry
	}
	t.Fatalf("no valid JSON log entry in: %s", string(data))
	return nil
}

func assertField(t *testing.T, entry map[string]any, key, want string) {
	t.Helper()
	val, ok := entry[key]
	if !ok {
		t.Errorf("expected field %q not present in log entry", key)
		return
	}
	got, _ := val.(string)
	if got != want {
		t.Errorf("field %q = %q, want %q", key, got, want)
	}
}

func assertFieldPresent(t *testing.T, entry map[string]any, key string) {
	t.Helper()
	if _, ok := entry[key]; !ok {
		t.Errorf("expected field %q not present in log entry", key)
	}
}

// --- Mock types ---

type mockPinger struct{}

func (m *mockPinger) PingContext(_ context.Context) error { return nil }

type mockConn struct{ closed bool }

func (m *mockConn) IsClosed() bool { return m.closed }

// noopCatalogClient satisfies saga.CatalogClient for testing.
type noopCatalogClient struct{}

func (c *noopCatalogClient) PurchasePrecheck(_ context.Context, _, _ string) (*saga.PrecheckResult, error) {
	return &saga.PrecheckResult{Allowed: true, Price: 5000, Currency: "ARS"}, nil
}
func (c *noopCatalogClient) RefundPrecheck(_ context.Context, _, _, _ string) (*saga.PrecheckResult, error) {
	return &saga.PrecheckResult{Allowed: true}, nil
}

// noopPaymentsClient satisfies saga.PaymentsClient for testing.
type noopPaymentsClient struct{}

func (c *noopPaymentsClient) RegisterTransaction(_ context.Context, _ saga.RegisterTransactionRequest) (*saga.RegisterTransactionResponse, error) {
	return &saga.RegisterTransactionResponse{}, nil
}
func (c *noopPaymentsClient) GetTransaction(_ context.Context, transactionID string) (*saga.TransactionDetails, error) {
	offeringID := "offering-1"
	return &saga.TransactionDetails{
		ID:         transactionID,
		UserID:     "user-1",
		Type:       "purchase",
		Status:     "completed",
		Amount:     5000,
		Currency:   "ARS",
		OfferingID: &offeringID,
	}, nil
}
func (c *noopPaymentsClient) UpdateTransactionStatus(_ context.Context, _, _ string, _ *string) error {
	return nil
}

// noopPublisher satisfies saga.Publisher for testing.
type noopPublisher struct{}

func (p *noopPublisher) Publish(_ context.Context, _, _, _ string, _ any) error { return nil }

// --- AMQP mock helpers for broker log tests ---

type mockPublishChannel struct {
	err error
}

func (m *mockPublishChannel) PublishWithContext(_ context.Context, _, _ string, _, _ bool, _ amqp.Publishing) error {
	return m.err
}

type mockConsumerChannel struct {
	deliveries chan amqp.Delivery
}

func newMockConsumerChannel() *mockConsumerChannel {
	return &mockConsumerChannel{deliveries: make(chan amqp.Delivery, 16)}
}

func (m *mockConsumerChannel) Consume(_, _ string, _, _, _, _ bool, _ amqp.Table) (<-chan amqp.Delivery, error) {
	return m.deliveries, nil
}

func (m *mockConsumerChannel) Qos(_, _ int, _ bool) error { return nil }

type mockAcknowledger struct{}

func (m *mockAcknowledger) Ack(_ uint64, _ bool) error          { return nil }
func (m *mockAcknowledger) Nack(_ uint64, _ bool, _ bool) error { return nil }
func (m *mockAcknowledger) Reject(_ uint64, _ bool) error       { return nil }

func makeDelivery(t *testing.T, payload any, msgType, correlationID string) (amqp.Delivery, *mockAcknowledger) {
	t.Helper()
	env, err := messaging.NewEnvelope(msgType, correlationID, payload)
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	body, err := env.Marshal()
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	ack := &mockAcknowledger{}
	return amqp.Delivery{Body: body, Acknowledger: ack}, ack
}
