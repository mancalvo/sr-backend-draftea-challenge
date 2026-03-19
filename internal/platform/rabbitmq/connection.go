// Package rabbitmq provides a thin wrapper around the amqp091-go client
// for RabbitMQ connection management, topology declaration, publishing,
// and consuming.
package rabbitmq

import (
	"fmt"
	"log/slog"
	"net/url"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Connection wraps an AMQP connection and exposes helpers for channel creation.
type Connection struct {
	conn   *amqp.Connection
	logger *slog.Logger
}

// Dial establishes a new AMQP connection to the given URL.
func Dial(url string, logger *slog.Logger) (*Connection, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq dial: %w", err)
	}
	logger.Info("rabbitmq connected", "url", sanitizeURL(url))
	return &Connection{conn: conn, logger: logger}, nil
}

// DialWithRetry establishes an AMQP connection, retrying with exponential
// backoff if the broker is not yet available. This is necessary in Docker
// Compose environments where the application container may start before
// RabbitMQ's AMQP listener is ready.
func DialWithRetry(rawURL string, logger *slog.Logger, maxAttempts int, baseDelay time.Duration) (*Connection, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		conn, err := amqp.Dial(rawURL)
		if err == nil {
			logger.Info("rabbitmq connected", "url", sanitizeURL(rawURL), "attempt", attempt)
			return &Connection{conn: conn, logger: logger}, nil
		}
		lastErr = err
		if attempt < maxAttempts {
			delay := baseDelay * time.Duration(1<<(attempt-1)) // exponential backoff
			logger.Warn("rabbitmq not ready, retrying",
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"delay", delay,
				"error", err,
			)
			time.Sleep(delay)
		}
	}
	return nil, fmt.Errorf("rabbitmq dial after %d attempts: %w", maxAttempts, lastErr)
}

// Channel opens a new AMQP channel on the underlying connection.
func (c *Connection) Channel() (*amqp.Channel, error) {
	ch, err := c.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq open channel: %w", err)
	}
	return ch, nil
}

// Close closes the underlying AMQP connection.
func (c *Connection) Close() error {
	if c.conn != nil && !c.conn.IsClosed() {
		return c.conn.Close()
	}
	return nil
}

// IsClosed returns true if the underlying AMQP connection is closed.
func (c *Connection) IsClosed() bool {
	return c.conn == nil || c.conn.IsClosed()
}

// sanitizeURL masks the password in an AMQP URL for safe logging.
func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "****")
	}
	return u.String()
}
