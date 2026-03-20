package rabbitmq

import (
	"errors"
	"fmt"
	"log/slog"
)

type runtimeChannel interface {
	ChannelDeclarer
	ChannelPublisher
	ChannelConsumer
	Close() error
}

type channelCloser interface {
	Close() error
}

// Runtime groups the long-lived RabbitMQ infrastructure a service needs after
// the shared topology has been declared.
type Runtime struct {
	Publisher *Publisher
	Consumer  *Consumer

	pubCh  channelCloser
	consCh channelCloser
}

// OpenRuntime declares the shared topology and opens the publisher and consumer
// channels used during the service lifecycle.
func OpenRuntime(conn *Connection, logger *slog.Logger, retry RetryConfig) (*Runtime, error) {
	return openRuntime(func() (runtimeChannel, error) {
		return conn.Channel()
	}, logger, retry)
}

func openRuntime(openChannel func() (runtimeChannel, error), logger *slog.Logger, retry RetryConfig) (*Runtime, error) {
	topoCh, err := openChannel()
	if err != nil {
		return nil, fmt.Errorf("open topology channel: %w", err)
	}
	if err := DeclareTopology(topoCh, DefaultTopology(), logger); err != nil {
		return nil, closeWithError(fmt.Errorf("declare topology: %w", err), topoCh)
	}
	if err := topoCh.Close(); err != nil {
		return nil, fmt.Errorf("close topology channel: %w", err)
	}

	pubCh, err := openChannel()
	if err != nil {
		return nil, fmt.Errorf("open publisher channel: %w", err)
	}

	consCh, err := openChannel()
	if err != nil {
		return nil, closeWithError(fmt.Errorf("open consumer channel: %w", err), pubCh)
	}

	return &Runtime{
		Publisher: NewPublisher(pubCh, logger),
		Consumer:  NewConsumer(consCh, logger, retry),
		pubCh:     pubCh,
		consCh:    consCh,
	}, nil
}

// Close closes the long-lived runtime channels.
func (r *Runtime) Close() error {
	return errors.Join(
		closeIfPresent(r.consCh),
		closeIfPresent(r.pubCh),
	)
}

func closeIfPresent(closer channelCloser) error {
	if closer == nil {
		return nil
	}
	return closer.Close()
}

func closeWithError(err error, closers ...channelCloser) error {
	closeErrs := make([]error, 0, len(closers)+1)
	closeErrs = append(closeErrs, err)
	for _, closer := range closers {
		closeErrs = append(closeErrs, closeIfPresent(closer))
	}
	return errors.Join(closeErrs...)
}
