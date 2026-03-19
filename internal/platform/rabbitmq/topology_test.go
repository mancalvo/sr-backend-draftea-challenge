package rabbitmq

import (
	"fmt"
	"log/slog"
	"os"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// mockChannel records all exchange, queue, and binding declarations for
// verification in tests.
type mockChannel struct {
	exchanges []exchangeCall
	queues    []queueCall
	bindings  []bindCall

	// If set, the corresponding method returns this error.
	exchangeErr error
	queueErr    error
	bindErr     error
}

type exchangeCall struct {
	Name    string
	Kind    string
	Durable bool
}

type queueCall struct {
	Name    string
	Durable bool
	Args    amqp.Table
}

type bindCall struct {
	Queue    string
	Key      string
	Exchange string
}

func (m *mockChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error {
	if m.exchangeErr != nil {
		return m.exchangeErr
	}
	m.exchanges = append(m.exchanges, exchangeCall{Name: name, Kind: kind, Durable: durable})
	return nil
}

func (m *mockChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	if m.queueErr != nil {
		return amqp.Queue{}, m.queueErr
	}
	m.queues = append(m.queues, queueCall{Name: name, Durable: durable, Args: args})
	return amqp.Queue{Name: name}, nil
}

func (m *mockChannel) QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error {
	if m.bindErr != nil {
		return m.bindErr
	}
	m.bindings = append(m.bindings, bindCall{Queue: name, Key: key, Exchange: exchange})
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestDefaultTopology_Exchanges(t *testing.T) {
	topo := DefaultTopology()
	expected := []string{
		messaging.ExchangeCommands,
		messaging.ExchangeOutcomes,
		messaging.ExchangeDLX,
	}
	if len(topo.Exchanges) != len(expected) {
		t.Fatalf("got %d exchanges, want %d", len(topo.Exchanges), len(expected))
	}
	expectedKinds := []string{"topic", "topic", "fanout"}
	for i, ex := range topo.Exchanges {
		if ex.Name != expected[i] {
			t.Errorf("exchange[%d].Name = %q, want %q", i, ex.Name, expected[i])
		}
		if ex.Kind != expectedKinds[i] {
			t.Errorf("exchange[%d].Kind = %q, want %q", i, ex.Kind, expectedKinds[i])
		}
		if !ex.Durable {
			t.Errorf("exchange[%d].Durable = false, want true", i)
		}
	}
}

func TestDefaultTopology_Queues(t *testing.T) {
	topo := DefaultTopology()
	expectedQueues := []string{
		messaging.QueuePaymentsCommands,
		messaging.QueueWalletsCommands,
		messaging.QueueCatalogAccessCommands,
		messaging.QueueSagaOutcomes,
		messaging.QueueDeadLetter,
	}
	if len(topo.Queues) != len(expectedQueues) {
		t.Fatalf("got %d queues, want %d", len(topo.Queues), len(expectedQueues))
	}
	for i, q := range topo.Queues {
		if q.Name != expectedQueues[i] {
			t.Errorf("queue[%d].Name = %q, want %q", i, q.Name, expectedQueues[i])
		}
		if !q.Durable {
			t.Errorf("queue[%d].Durable = false, want true", i)
		}
	}
}

func TestDefaultTopology_DLXArgs(t *testing.T) {
	topo := DefaultTopology()
	for _, q := range topo.Queues {
		// The dead-letter queue itself is not routed to a DLX.
		if q.Name == messaging.QueueDeadLetter {
			continue
		}
		dlx, ok := q.Args["x-dead-letter-exchange"]
		if !ok {
			t.Errorf("queue %q missing x-dead-letter-exchange arg", q.Name)
			continue
		}
		if dlx != messaging.ExchangeDLX {
			t.Errorf("queue %q DLX = %q, want %q", q.Name, dlx, messaging.ExchangeDLX)
		}
	}
}

func TestDefaultTopology_QueueBindings(t *testing.T) {
	topo := DefaultTopology()
	expected := map[string][]string{
		messaging.QueuePaymentsCommands: {
			messaging.RoutingKeyDepositRequested,
		},
		messaging.QueueWalletsCommands: {
			messaging.RoutingKeyWalletDebitRequested,
			messaging.RoutingKeyWalletCreditRequested,
		},
		messaging.QueueCatalogAccessCommands: {
			messaging.RoutingKeyAccessGrantRequested,
			messaging.RoutingKeyAccessRevokeRequested,
		},
		messaging.QueueSagaOutcomes: {
			messaging.RoutingKeyWalletDebited,
			messaging.RoutingKeyWalletDebitRejected,
			messaging.RoutingKeyWalletCredited,
			messaging.RoutingKeyAccessGranted,
			messaging.RoutingKeyAccessGrantConflicted,
			messaging.RoutingKeyAccessRevoked,
			messaging.RoutingKeyAccessRevokeRejected,
			messaging.RoutingKeyProviderChargeSucceeded,
			messaging.RoutingKeyProviderChargeFailed,
		},
		messaging.QueueDeadLetter: {}, // fanout exchange, no routing keys
	}
	for _, q := range topo.Queues {
		want, ok := expected[q.Name]
		if !ok {
			t.Errorf("unexpected queue %q", q.Name)
			continue
		}
		if len(q.RoutingKeys) != len(want) {
			t.Errorf("queue %q: got %d routing keys, want %d", q.Name, len(q.RoutingKeys), len(want))
			continue
		}
		for i, key := range q.RoutingKeys {
			if key != want[i] {
				t.Errorf("queue %q routing_keys[%d] = %q, want %q", q.Name, i, key, want[i])
			}
		}
	}
}

func TestDeclareTopology_CallsAllDeclarations(t *testing.T) {
	ch := &mockChannel{}
	topo := DefaultTopology()

	if err := DeclareTopology(ch, topo, testLogger()); err != nil {
		t.Fatalf("DeclareTopology returned error: %v", err)
	}

	if len(ch.exchanges) != len(topo.Exchanges) {
		t.Errorf("declared %d exchanges, want %d", len(ch.exchanges), len(topo.Exchanges))
	}
	if len(ch.queues) != len(topo.Queues) {
		t.Errorf("declared %d queues, want %d", len(ch.queues), len(topo.Queues))
	}

	// Count total expected bindings.
	totalBindings := 0
	for _, q := range topo.Queues {
		totalBindings += len(q.RoutingKeys)
	}
	if len(ch.bindings) != totalBindings {
		t.Errorf("declared %d bindings, want %d", len(ch.bindings), totalBindings)
	}
}

func TestDeclareTopology_ExchangeError(t *testing.T) {
	ch := &mockChannel{exchangeErr: fmt.Errorf("exchange boom")}
	topo := DefaultTopology()

	err := DeclareTopology(ch, topo, testLogger())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := `declare exchange "workflow.commands": exchange boom`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestDeclareTopology_QueueError(t *testing.T) {
	ch := &mockChannel{queueErr: fmt.Errorf("queue boom")}
	topo := DefaultTopology()

	err := DeclareTopology(ch, topo, testLogger())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := `declare queue "payments.commands": queue boom`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestDeclareTopology_BindError(t *testing.T) {
	ch := &mockChannel{bindErr: fmt.Errorf("bind boom")}
	topo := DefaultTopology()

	err := DeclareTopology(ch, topo, testLogger())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := `bind queue "payments.commands" to "workflow.commands" with key "payments.deposit.requested": bind boom`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
