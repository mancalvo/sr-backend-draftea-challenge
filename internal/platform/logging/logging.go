// Package logging provides structured logging helpers built on log/slog.
//
// All loggers include a "service" attribute. Contextual fields like
// transaction_id, correlation_id, and message_id can be added via
// context or by deriving child loggers with With.
package logging

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

// New creates a new structured JSON logger for the given service name.
func New(service string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(handler).With(slog.String("service", service))
}

// WithContext returns a new context carrying the given logger.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext extracts the logger from the context, falling back to a default
// logger if none is present.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// With returns a child logger with additional structured attributes.
func With(logger *slog.Logger, attrs ...any) *slog.Logger {
	return logger.With(attrs...)
}

// Common attribute keys used across services.
const (
	KeyService       = "service"
	KeyTransactionID = "transaction_id"
	KeyCorrelationID = "correlation_id"
	KeyMessageID     = "message_id"
)
