package processdeposit

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

// Provider executes the external provider charge.
type Provider interface {
	Charge(ctx context.Context, transactionID, userID string, amount int64, currency string) (*ChargeResult, error)
}

// ChargeResult holds the outcome of a provider charge attempt.
type ChargeResult struct {
	Success     bool
	ProviderRef string
	Reason      string
}

// UseCase processes deposit requests through the provider.
type UseCase struct {
	provider  Provider
	publisher Publisher
	logger    *slog.Logger
}

// New creates a new deposit-processing use case.
func New(provider Provider, publisher Publisher, logger *slog.Logger) *UseCase {
	return &UseCase{provider: provider, publisher: publisher, logger: logger}
}

// Execute processes a payments.deposit.requested command and publishes the corresponding outcome.
func (u *UseCase) Execute(ctx context.Context, env messaging.Envelope, cmd messaging.DepositRequested) error {
	logger := u.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.Int64("amount", cmd.Amount),
		slog.String("currency", cmd.Currency),
	)

	logger.Info("processing deposit request via provider")

	result, err := u.provider.Charge(ctx, cmd.TransactionID, cmd.UserID, cmd.Amount, cmd.Currency)
	if err != nil {
		logger.Error("provider charge error", "error", err)
		return fmt.Errorf("provider charge: %w", err)
	}

	if result.Success {
		logger.Info("provider charge succeeded", "provider_ref", result.ProviderRef)
		return u.publisher.Publish(ctx,
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
	return u.publisher.Publish(ctx,
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
