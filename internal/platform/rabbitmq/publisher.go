package rabbitmq

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// Publisher provides a thin wrapper for publishing typed messages to RabbitMQ
// exchanges using the standard messaging.Envelope format.
type Publisher struct {
	ch     ChannelPublisher
	logger *slog.Logger
}

// ChannelPublisher is the subset of amqp.Channel needed for publishing.
// Introducing this interface allows unit-testing without a real broker.
type ChannelPublisher interface {
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
}

// NewPublisher creates a Publisher backed by the given channel.
func NewPublisher(ch ChannelPublisher, logger *slog.Logger) *Publisher {
	return &Publisher{ch: ch, logger: logger}
}

// Publish wraps the payload in a messaging.Envelope, serializes it, and
// publishes to the specified exchange with the given routing key.
func (p *Publisher) Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error {
	env, err := messaging.NewEnvelope(routingKey, correlationID, payload)
	if err != nil {
		return fmt.Errorf("publisher: create envelope: %w", err)
	}

	body, err := env.Marshal()
	if err != nil {
		return fmt.Errorf("publisher: marshal envelope: %w", err)
	}

	pub := amqp.Publishing{
		ContentType:   "application/json",
		DeliveryMode:  amqp.Persistent,
		Timestamp:     time.Now().UTC(),
		MessageId:     env.MessageID,
		CorrelationId: env.CorrelationID,
		Type:          env.Type,
		Body:          body,
	}

	if err := p.ch.PublishWithContext(ctx, exchange, routingKey, false, false, pub); err != nil {
		return fmt.Errorf("publisher: publish to %s/%s: %w", exchange, routingKey, err)
	}

	p.logger.Info("message published",
		"exchange", exchange,
		"routing_key", routingKey,
		"message_id", env.MessageID,
		"correlation_id", env.CorrelationID,
	)

	return nil
}
