package rabbitmq

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestOpenRuntime_Success(t *testing.T) {
	t.Parallel()

	channels := []*fakeRuntimeChannel{
		{name: "topology"},
		{name: "publisher"},
		{name: "consumer"},
	}
	openCalls := 0

	runtime, err := openRuntime(func() (runtimeChannel, error) {
		ch := channels[openCalls]
		openCalls++
		return ch, nil
	}, testRabbitLogger(), DefaultRetryConfig())
	if err != nil {
		t.Fatalf("openRuntime: %v", err)
	}

	if runtime.Publisher == nil {
		t.Fatal("Publisher should not be nil")
	}
	if runtime.Consumer == nil {
		t.Fatal("Consumer should not be nil")
	}
	if channels[0].closeCalls != 1 {
		t.Fatalf("topology close calls = %d, want 1", channels[0].closeCalls)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("runtime.Close: %v", err)
	}
	if channels[1].closeCalls != 1 {
		t.Fatalf("publisher close calls = %d, want 1", channels[1].closeCalls)
	}
	if channels[2].closeCalls != 1 {
		t.Fatalf("consumer close calls = %d, want 1", channels[2].closeCalls)
	}
}

func TestOpenRuntime_ClosesOpenedChannelsOnFailure(t *testing.T) {
	t.Parallel()

	topology := &fakeRuntimeChannel{name: "topology"}
	publisher := &fakeRuntimeChannel{name: "publisher"}
	expectedErr := errors.New("consumer channel failed")
	openCalls := 0

	_, err := openRuntime(func() (runtimeChannel, error) {
		switch openCalls {
		case 0:
			openCalls++
			return topology, nil
		case 1:
			openCalls++
			return publisher, nil
		default:
			return nil, expectedErr
		}
	}, testRabbitLogger(), DefaultRetryConfig())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("error = %v, want %v", err, expectedErr)
	}
	if publisher.closeCalls != 1 {
		t.Fatalf("publisher close calls = %d, want 1", publisher.closeCalls)
	}
}

func TestOpenRuntime_ClosesTopologyChannelOnDeclarationError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("declare exchange failed")
	topology := &fakeRuntimeChannel{
		name:               "topology",
		exchangeDeclareErr: expectedErr,
	}

	_, err := openRuntime(func() (runtimeChannel, error) {
		return topology, nil
	}, testRabbitLogger(), DefaultRetryConfig())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("error = %v, want %v", err, expectedErr)
	}
	if topology.closeCalls != 1 {
		t.Fatalf("topology close calls = %d, want 1", topology.closeCalls)
	}
}

type fakeRuntimeChannel struct {
	name               string
	closeCalls         int
	exchangeDeclareErr error
}

func (f *fakeRuntimeChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error {
	return f.exchangeDeclareErr
}

func (f *fakeRuntimeChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	return amqp.Queue{Name: name}, nil
}

func (f *fakeRuntimeChannel) QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error {
	return nil
}

func (f *fakeRuntimeChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	return nil
}

func (f *fakeRuntimeChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	return make(chan amqp.Delivery), nil
}

func (f *fakeRuntimeChannel) Qos(prefetchCount, prefetchSize int, global bool) error {
	return nil
}

func (f *fakeRuntimeChannel) Close() error {
	f.closeCalls++
	return nil
}

func testRabbitLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
