package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// mockConsumerChannel lets tests control what deliveries appear and verifies
// Qos/Consume calls.
type mockConsumerChannel struct {
	deliveries chan amqp.Delivery
	qosCalled  bool
	qosErr     error
	consumeErr error
}

func newMockConsumerChannel() *mockConsumerChannel {
	return &mockConsumerChannel{
		deliveries: make(chan amqp.Delivery, 16),
	}
}

func (m *mockConsumerChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	if m.consumeErr != nil {
		return nil, m.consumeErr
	}
	return m.deliveries, nil
}

func (m *mockConsumerChannel) Qos(prefetchCount, prefetchSize int, global bool) error {
	m.qosCalled = true
	return m.qosErr
}

// mockAcknowledger records ack/nack calls for assertion.
type mockAcknowledger struct {
	acked    bool
	nacked   bool
	requeued bool
}

func (m *mockAcknowledger) Ack(tag uint64, multiple bool) error {
	m.acked = true
	return nil
}

func (m *mockAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	m.nacked = true
	m.requeued = requeue
	return nil
}

func (m *mockAcknowledger) Reject(tag uint64, requeue bool) error {
	m.nacked = true
	m.requeued = requeue
	return nil
}

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
	return amqp.Delivery{
		Body:         body,
		Acknowledger: ack,
	}, ack
}

func TestConsumer_Consume_Success(t *testing.T) {
	ch := newMockConsumerChannel()
	retry := RetryConfig{MaxRetries: 0, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	consumer := NewConsumer(ch, testLogger(), retry)

	payload := messaging.WalletDebitRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        1000,
		Currency:      "ARS",
		SourceStep:    "purchase_debit",
	}

	delivery, ack := makeDelivery(t, payload, messaging.RoutingKeyWalletDebitRequested, "corr-1")

	var received messaging.Envelope
	handler := func(ctx context.Context, env messaging.Envelope) error {
		received = env
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Send delivery then close channel after a brief pause to stop loop.
	go func() {
		ch.deliveries <- delivery
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := consumer.Consume(ctx, messaging.QueueWalletsCommands, handler)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	if !ack.acked {
		t.Error("message should have been acked")
	}
	if received.Type != messaging.RoutingKeyWalletDebitRequested {
		t.Errorf("received type = %q, want %q", received.Type, messaging.RoutingKeyWalletDebitRequested)
	}
}

func TestConsumer_Consume_HandlerError_Retries(t *testing.T) {
	ch := newMockConsumerChannel()
	retry := RetryConfig{MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	consumer := NewConsumer(ch, testLogger(), retry)

	delivery, ack := makeDelivery(t, map[string]string{"k": "v"}, "test.type", "corr-1")

	attempts := 0
	handler := func(ctx context.Context, env messaging.Envelope) error {
		attempts++
		return fmt.Errorf("handler error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch.deliveries <- delivery
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	_ = consumer.Consume(ctx, "test.queue", handler)

	// Should have attempted 1 initial + 2 retries = 3.
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if !ack.nacked {
		t.Error("message should have been nacked after retries exhausted")
	}
	if ack.requeued {
		t.Error("message should NOT have been requeued (should go to DLX)")
	}
}

func TestConsumer_Consume_MalformedMessage(t *testing.T) {
	ch := newMockConsumerChannel()
	retry := DefaultRetryConfig()
	consumer := NewConsumer(ch, testLogger(), retry)

	ack := &mockAcknowledger{}
	delivery := amqp.Delivery{
		Body:         []byte(`{not valid json`),
		Acknowledger: ack,
	}

	handler := func(ctx context.Context, env messaging.Envelope) error {
		t.Fatal("handler should not be called for malformed messages")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch.deliveries <- delivery
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = consumer.Consume(ctx, "test.queue", handler)

	if !ack.nacked {
		t.Error("malformed message should have been nacked")
	}
	if ack.requeued {
		t.Error("malformed message should NOT have been requeued")
	}
}

func TestConsumer_Consume_QosError(t *testing.T) {
	ch := newMockConsumerChannel()
	ch.qosErr = fmt.Errorf("qos boom")
	consumer := NewConsumer(ch, testLogger(), DefaultRetryConfig())

	err := consumer.Consume(context.Background(), "test.queue", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestConsumer_Consume_ConsumeError(t *testing.T) {
	ch := newMockConsumerChannel()
	ch.consumeErr = fmt.Errorf("consume boom")
	consumer := NewConsumer(ch, testLogger(), DefaultRetryConfig())

	err := consumer.Consume(context.Background(), "test.queue", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestConsumer_DeliveryChannelClosed(t *testing.T) {
	ch := newMockConsumerChannel()
	consumer := NewConsumer(ch, testLogger(), DefaultRetryConfig())

	close(ch.deliveries)

	handler := func(ctx context.Context, env messaging.Envelope) error {
		return nil
	}

	err := consumer.Consume(context.Background(), "test.queue", handler)
	if err == nil {
		t.Fatal("expected error when delivery channel closes, got nil")
	}
}

func TestBackoffDelay(t *testing.T) {
	base := 500 * time.Millisecond
	max := 5 * time.Second

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 500 * time.Millisecond},  // 500ms * 2^0
		{2, 1000 * time.Millisecond}, // 500ms * 2^1
		{3, 2000 * time.Millisecond}, // 500ms * 2^2
		{4, 4000 * time.Millisecond}, // 500ms * 2^3
		{5, 5 * time.Second},         // capped at max
		{10, 5 * time.Second},        // still capped
	}
	for _, tc := range tests {
		got := backoffDelay(tc.attempt, base, max)
		if got != tc.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestConsumer_Consume_HandlerSucceedsOnRetry(t *testing.T) {
	ch := newMockConsumerChannel()
	retry := RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	consumer := NewConsumer(ch, testLogger(), retry)

	delivery, ack := makeDelivery(t, map[string]string{"k": "v"}, "test.type", "corr-1")

	attempts := 0
	handler := func(ctx context.Context, env messaging.Envelope) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("transient error")
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch.deliveries <- delivery
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	_ = consumer.Consume(ctx, "test.queue", handler)

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if !ack.acked {
		t.Error("message should have been acked after successful retry")
	}
}

// TestDefaultRetryConfig verifies the documented defaults.
func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.BaseDelay != 500*time.Millisecond {
		t.Errorf("BaseDelay = %v, want 500ms", cfg.BaseDelay)
	}
	if cfg.MaxDelay != 5*time.Second {
		t.Errorf("MaxDelay = %v, want 5s", cfg.MaxDelay)
	}
}

// Verify that makeDelivery produces valid JSON.
func TestMakeDelivery_ProducesValidJSON(t *testing.T) {
	d, _ := makeDelivery(t, map[string]string{"test": "val"}, "test.type", "corr-1")
	if !json.Valid(d.Body) {
		t.Error("delivery body should be valid JSON")
	}
}
