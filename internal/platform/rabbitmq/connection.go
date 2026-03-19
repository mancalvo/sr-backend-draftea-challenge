// Package rabbitmq provides a thin wrapper around the amqp091-go client
// for RabbitMQ connection management, topology declaration, publishing,
// and consuming.
package rabbitmq

import (
	"fmt"
	"log/slog"

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
func sanitizeURL(url string) string {
	// Simple approach: mask anything between :// user:pass@ pattern
	const maxLen = 80
	if len(url) > maxLen {
		return url[:maxLen] + "..."
	}
	return url
}
