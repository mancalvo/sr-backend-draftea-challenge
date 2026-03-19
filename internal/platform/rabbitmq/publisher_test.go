package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// mockPublishChannel records publish calls for verification.
type mockPublishChannel struct {
	published []publishCall
	err       error
}

type publishCall struct {
	Exchange   string
	RoutingKey string
	Msg        amqp.Publishing
}

func (m *mockPublishChannel) PublishWithContext(_ context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	if m.err != nil {
		return m.err
	}
	m.published = append(m.published, publishCall{Exchange: exchange, RoutingKey: key, Msg: msg})
	return nil
}

func TestPublisher_Publish_Success(t *testing.T) {
	ch := &mockPublishChannel{}
	pub := NewPublisher(ch, testLogger())

	payload := messaging.DepositRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        5000,
		Currency:      "ARS",
	}

	err := pub.Publish(context.Background(), messaging.ExchangeCommands, messaging.RoutingKeyDepositRequested, "corr-1", payload)
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if len(ch.published) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(ch.published))
	}

	call := ch.published[0]
	if call.Exchange != messaging.ExchangeCommands {
		t.Errorf("exchange = %q, want %q", call.Exchange, messaging.ExchangeCommands)
	}
	if call.RoutingKey != messaging.RoutingKeyDepositRequested {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyDepositRequested)
	}
	if call.Msg.ContentType != "application/json" {
		t.Errorf("content type = %q, want %q", call.Msg.ContentType, "application/json")
	}
	if call.Msg.DeliveryMode != amqp.Persistent {
		t.Errorf("delivery mode = %d, want %d", call.Msg.DeliveryMode, amqp.Persistent)
	}

	// Verify body is a valid envelope containing the payload.
	var env messaging.Envelope
	if err := json.Unmarshal(call.Msg.Body, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.CorrelationID != "corr-1" {
		t.Errorf("correlation_id = %q, want %q", env.CorrelationID, "corr-1")
	}
	if env.Type != messaging.RoutingKeyDepositRequested {
		t.Errorf("type = %q, want %q", env.Type, messaging.RoutingKeyDepositRequested)
	}

	var decoded messaging.DepositRequested
	if err := env.DecodePayload(&decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded.TransactionID != "txn-1" {
		t.Errorf("TransactionID = %q, want %q", decoded.TransactionID, "txn-1")
	}
	if decoded.Amount != 5000 {
		t.Errorf("Amount = %d, want %d", decoded.Amount, 5000)
	}
}

func TestPublisher_Publish_ChannelError(t *testing.T) {
	ch := &mockPublishChannel{err: fmt.Errorf("channel closed")}
	pub := NewPublisher(ch, testLogger())

	err := pub.Publish(context.Background(), "ex", "key", "corr", map[string]string{"k": "v"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPublisher_Publish_InvalidPayload(t *testing.T) {
	ch := &mockPublishChannel{}
	pub := NewPublisher(ch, testLogger())

	err := pub.Publish(context.Background(), "ex", "key", "corr", make(chan int))
	if err == nil {
		t.Fatal("expected error for non-marshallable payload, got nil")
	}
}

func TestPublisher_AMQPHeaderFields(t *testing.T) {
	ch := &mockPublishChannel{}
	pub := NewPublisher(ch, testLogger())

	err := pub.Publish(context.Background(), "ex", "key", "corr-abc", "hello")
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	call := ch.published[0]
	if call.Msg.CorrelationId != "corr-abc" {
		t.Errorf("CorrelationId = %q, want %q", call.Msg.CorrelationId, "corr-abc")
	}
	if call.Msg.Type != "key" {
		t.Errorf("Type = %q, want %q", call.Msg.Type, "key")
	}
	if call.Msg.MessageId == "" {
		t.Error("MessageId should not be empty")
	}
	if call.Msg.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}
