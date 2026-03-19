package rabbitmq

import (
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// ExchangeDeclaration defines an exchange to be declared.
type ExchangeDeclaration struct {
	Name    string
	Kind    string // "topic", "direct", "fanout", "headers"
	Durable bool
}

// QueueDeclaration defines a queue to be declared with its bindings.
type QueueDeclaration struct {
	Name        string
	Durable     bool
	Exchange    string
	RoutingKeys []string
	Args        amqp.Table // optional arguments (e.g. x-dead-letter-exchange)
}

// Topology holds the full set of exchanges and queues that must exist.
type Topology struct {
	Exchanges []ExchangeDeclaration
	Queues    []QueueDeclaration
}

// DefaultTopology returns the standard topology for the payment system.
func DefaultTopology() Topology {
	return Topology{
		Exchanges: []ExchangeDeclaration{
			{Name: messaging.ExchangeCommands, Kind: "topic", Durable: true},
			{Name: messaging.ExchangeOutcomes, Kind: "topic", Durable: true},
			{Name: messaging.ExchangeDLX, Kind: "fanout", Durable: true},
		},
		Queues: []QueueDeclaration{
			{
				Name:     messaging.QueuePaymentsCommands,
				Durable:  true,
				Exchange: messaging.ExchangeCommands,
				RoutingKeys: []string{
					messaging.RoutingKeyDepositRequested,
				},
				Args: amqp.Table{
					"x-dead-letter-exchange": messaging.ExchangeDLX,
				},
			},
			{
				Name:     messaging.QueueWalletsCommands,
				Durable:  true,
				Exchange: messaging.ExchangeCommands,
				RoutingKeys: []string{
					messaging.RoutingKeyWalletDebitRequested,
					messaging.RoutingKeyWalletCreditRequested,
				},
				Args: amqp.Table{
					"x-dead-letter-exchange": messaging.ExchangeDLX,
				},
			},
			{
				Name:     messaging.QueueCatalogAccessCommands,
				Durable:  true,
				Exchange: messaging.ExchangeCommands,
				RoutingKeys: []string{
					messaging.RoutingKeyAccessGrantRequested,
					messaging.RoutingKeyAccessRevokeRequested,
				},
				Args: amqp.Table{
					"x-dead-letter-exchange": messaging.ExchangeDLX,
				},
			},
			{
				Name:     messaging.QueueSagaOutcomes,
				Durable:  true,
				Exchange: messaging.ExchangeOutcomes,
				RoutingKeys: []string{
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
				Args: amqp.Table{
					"x-dead-letter-exchange": messaging.ExchangeDLX,
				},
			},
			// Catch-all dead-letter queue bound to the DLX fanout exchange.
			{
				Name:     messaging.QueueDeadLetter,
				Durable:  true,
				Exchange: messaging.ExchangeDLX,
			},
		},
	}
}

// ChannelDeclarer is the subset of amqp.Channel methods needed for topology
// declaration. Introducing this interface allows unit-testing without a real
// RabbitMQ connection.
type ChannelDeclarer interface {
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
}

// DeclareTopology creates all exchanges, queues, and bindings defined in the
// given Topology on the provided channel.
func DeclareTopology(ch ChannelDeclarer, topo Topology, logger *slog.Logger) error {
	for _, ex := range topo.Exchanges {
		if err := ch.ExchangeDeclare(
			ex.Name,
			ex.Kind,
			ex.Durable,
			false, // autoDelete
			false, // internal
			false, // noWait
			nil,
		); err != nil {
			return fmt.Errorf("declare exchange %q: %w", ex.Name, err)
		}
		logger.Info("exchange declared", "exchange", ex.Name, "kind", ex.Kind)
	}

	for _, q := range topo.Queues {
		if _, err := ch.QueueDeclare(
			q.Name,
			q.Durable,
			false, // autoDelete
			false, // exclusive
			false, // noWait
			q.Args,
		); err != nil {
			return fmt.Errorf("declare queue %q: %w", q.Name, err)
		}
		logger.Info("queue declared", "queue", q.Name)

		for _, key := range q.RoutingKeys {
			if err := ch.QueueBind(
				q.Name,
				key,
				q.Exchange,
				false, // noWait
				nil,
			); err != nil {
				return fmt.Errorf("bind queue %q to %q with key %q: %w", q.Name, q.Exchange, key, err)
			}
			logger.Info("queue bound", "queue", q.Name, "exchange", q.Exchange, "routing_key", key)
		}
	}

	return nil
}
