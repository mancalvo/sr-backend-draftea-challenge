package payments

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// Publisher defines the subset of publishing operations needed by consumers.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// ConsumerHandler provides AMQP message handlers for payments commands.
type ConsumerHandler struct {
	provider  Provider
	publisher Publisher
	logger    *slog.Logger
}

// NewConsumerHandler creates a new ConsumerHandler.
func NewConsumerHandler(provider Provider, publisher Publisher, logger *slog.Logger) *ConsumerHandler {
	return &ConsumerHandler{provider: provider, publisher: publisher, logger: logger}
}

// HandleDepositRequested processes a payments.deposit.requested command.
// It calls the external provider to execute the charge and publishes
// either provider.charge.succeeded or provider.charge.failed as the outcome.
func (c *ConsumerHandler) HandleDepositRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.DepositRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode payments.deposit.requested: %w", err)
	}

	logger := c.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.Int64("amount", cmd.Amount),
		slog.String("currency", cmd.Currency),
	)

	logger.Info("processing deposit request via provider")

	result, err := c.provider.Charge(ctx, cmd.TransactionID, cmd.UserID, cmd.Amount, cmd.Currency)
	if err != nil {
		logger.Error("provider charge error", "error", err)
		return fmt.Errorf("provider charge: %w", err)
	}

	if result.Success {
		logger.Info("provider charge succeeded", "provider_ref", result.ProviderRef)
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyProviderChargeSucceeded,
			env.CorrelationID,
			messaging.ProviderChargeSucceeded{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				Amount:        cmd.Amount,
				ProviderRef:   result.ProviderRef,
			},
		)
	}

	logger.Warn("provider charge failed", "reason", result.Reason)
	return c.publisher.Publish(ctx,
		messaging.ExchangeOutcomes,
		messaging.RoutingKeyProviderChargeFailed,
		env.CorrelationID,
		messaging.ProviderChargeFailed{
			TransactionID: cmd.TransactionID,
			UserID:        cmd.UserID,
			Amount:        cmd.Amount,
			Reason:        result.Reason,
		},
	)
}
