package rabbitmq

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// RetryConfig controls exponential backoff for consumer retries.
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryConfig returns the standard retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   5 * time.Second,
	}
}

// HandlerFunc processes a decoded envelope. Returning an error signals a
// retryable failure; returning nil means the message was handled successfully.
type HandlerFunc func(ctx context.Context, env messaging.Envelope) error

// Consumer wraps an AMQP channel and provides a typed message consumption loop
// with automatic acknowledge/reject and optional retry backoff.
type Consumer struct {
	ch     ChannelConsumer
	logger *slog.Logger
	retry  RetryConfig
}

// ChannelConsumer is the subset of amqp.Channel needed for consuming.
type ChannelConsumer interface {
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	Qos(prefetchCount, prefetchSize int, global bool) error
}

// NewConsumer creates a Consumer backed by the given channel.
func NewConsumer(ch ChannelConsumer, logger *slog.Logger, retry RetryConfig) *Consumer {
	return &Consumer{ch: ch, logger: logger, retry: retry}
}

// Consume starts consuming from the given queue, calling the handler for each
// message. It blocks until the context is cancelled or the delivery channel
// is closed. Messages that fail after exhausting retries are rejected (nack
// without requeue) so they go to the DLX.
func (c *Consumer) Consume(ctx context.Context, queue string, handler HandlerFunc) error {
	if err := c.ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("consumer: set qos: %w", err)
	}

	deliveries, err := c.ch.Consume(
		queue,
		"",    // consumer tag (auto-generated)
		false, // autoAck
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("consumer: start consuming %q: %w", queue, err)
	}

	c.logger.Info("consumer started", "queue", queue)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("consumer stopping", "queue", queue, "reason", ctx.Err())
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				c.logger.Warn("delivery channel closed", "queue", queue)
				return fmt.Errorf("consumer: delivery channel closed for %q", queue)
			}
			c.handleDelivery(ctx, d, handler, queue)
		}
	}
}

// handleDelivery processes a single delivery with retry logic.
func (c *Consumer) handleDelivery(ctx context.Context, d amqp.Delivery, handler HandlerFunc, queue string) {
	env, err := messaging.UnmarshalEnvelope(d.Body)
	if err != nil {
		c.logger.Error("failed to unmarshal envelope",
			"queue", queue,
			"error", err,
		)
		// Reject without requeue: malformed messages go to DLX.
		_ = d.Nack(false, false)
		return
	}

	c.logger.Info("message received",
		"queue", queue,
		"message_id", env.MessageID,
		"correlation_id", env.CorrelationID,
		"type", env.Type,
	)

	var lastErr error
	for attempt := 0; attempt <= c.retry.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt, c.retry.BaseDelay, c.retry.MaxDelay)
			c.logger.Warn("retrying message",
				"queue", queue,
				"message_id", env.MessageID,
				"attempt", attempt,
				"delay", delay,
			)
			select {
			case <-ctx.Done():
				_ = d.Nack(false, true) // requeue on shutdown
				return
			case <-time.After(delay):
			}
		}

		if err := handler(ctx, env); err != nil {
			lastErr = err
			continue
		}

		// Success: acknowledge the message.
		if ackErr := d.Ack(false); ackErr != nil {
			c.logger.Error("failed to ack message",
				"queue", queue,
				"message_id", env.MessageID,
				"error", ackErr,
			)
		}
		return
	}

	// Exhausted all retries: reject without requeue -> DLX.
	c.logger.Error("message handling failed after retries",
		"queue", queue,
		"message_id", env.MessageID,
		"attempts", c.retry.MaxRetries+1,
		"error", lastErr,
	)
	_ = d.Nack(false, false)
}

// backoffDelay computes exponential backoff capped at maxDelay.
func backoffDelay(attempt int, base, max time.Duration) time.Duration {
	delay := time.Duration(float64(base) * math.Pow(2, float64(attempt-1)))
	if delay > max {
		return max
	}
	return delay
}
