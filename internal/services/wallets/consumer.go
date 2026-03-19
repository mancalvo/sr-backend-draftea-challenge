package wallets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// Publisher defines the subset of publishing operations needed by consumers.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// ConsumerHandler provides AMQP message handlers for wallets commands.
type ConsumerHandler struct {
	repo      Repository
	publisher Publisher
	logger    *slog.Logger
}

// NewConsumerHandler creates a new ConsumerHandler.
func NewConsumerHandler(repo Repository, publisher Publisher, logger *slog.Logger) *ConsumerHandler {
	return &ConsumerHandler{repo: repo, publisher: publisher, logger: logger}
}

// HandleWalletDebitRequested processes a wallet.debit.requested command.
// It attempts to debit the user's wallet, publishing either wallet.debited
// or wallet.debit.rejected as the outcome.
func (c *ConsumerHandler) HandleWalletDebitRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.WalletDebitRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode wallet.debit.requested: %w", err)
	}

	logger := c.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.Int64("amount", cmd.Amount),
		slog.String("currency", cmd.Currency),
		slog.String("source_step", cmd.SourceStep),
	)

	logger.Info("processing wallet debit request")

	result, err := c.repo.Debit(ctx, cmd.UserID, cmd.TransactionID, cmd.SourceStep, cmd.Amount)
	if errors.Is(err, ErrInsufficientFunds) {
		logger.Warn("wallet debit rejected: insufficient funds")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyWalletDebitRejected,
			env.CorrelationID,
			messaging.WalletDebitRejected{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				Amount:        cmd.Amount,
				Reason:        "insufficient funds",
				SourceStep:    cmd.SourceStep,
			},
		)
	}
	if errors.Is(err, ErrWalletNotFound) {
		logger.Warn("wallet debit rejected: wallet not found")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyWalletDebitRejected,
			env.CorrelationID,
			messaging.WalletDebitRejected{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				Amount:        cmd.Amount,
				Reason:        "wallet not found",
				SourceStep:    cmd.SourceStep,
			},
		)
	}
	if errors.Is(err, ErrDuplicateMovement) {
		// Idempotent: the debit was already applied. Publish success with current balance.
		// Re-fetch wallet to get current balance for the outcome.
		wallet, fetchErr := c.repo.GetWalletByUserID(ctx, cmd.UserID)
		if fetchErr != nil {
			logger.Error("failed to fetch wallet after duplicate debit", "error", fetchErr)
			return fmt.Errorf("fetch wallet after duplicate: %w", fetchErr)
		}
		logger.Info("duplicate debit request, returning idempotent success")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyWalletDebited,
			env.CorrelationID,
			messaging.WalletDebited{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				Amount:        cmd.Amount,
				BalanceAfter:  wallet.Balance,
				SourceStep:    cmd.SourceStep,
			},
		)
	}
	if err != nil {
		logger.Error("failed to debit wallet", "error", err)
		return fmt.Errorf("debit wallet: %w", err)
	}

	logger.Info("wallet debited successfully", "balance_after", result.Wallet.Balance)
	return c.publisher.Publish(ctx,
		messaging.ExchangeOutcomes,
		messaging.RoutingKeyWalletDebited,
		env.CorrelationID,
		messaging.WalletDebited{
			TransactionID: cmd.TransactionID,
			UserID:        cmd.UserID,
			Amount:        cmd.Amount,
			BalanceAfter:  result.Wallet.Balance,
			SourceStep:    cmd.SourceStep,
		},
	)
}

// HandleWalletCreditRequested processes a wallet.credit.requested command.
// It credits the user's wallet and publishes wallet.credited as the outcome.
func (c *ConsumerHandler) HandleWalletCreditRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.WalletCreditRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode wallet.credit.requested: %w", err)
	}

	logger := c.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.Int64("amount", cmd.Amount),
		slog.String("currency", cmd.Currency),
		slog.String("source_step", cmd.SourceStep),
	)

	logger.Info("processing wallet credit request")

	result, err := c.repo.Credit(ctx, cmd.UserID, cmd.TransactionID, cmd.SourceStep, cmd.Amount)
	if errors.Is(err, ErrWalletNotFound) {
		logger.Error("wallet credit failed: wallet not found")
		return fmt.Errorf("credit wallet: %w", err)
	}
	if errors.Is(err, ErrDuplicateMovement) {
		// Idempotent: the credit was already applied.
		wallet, fetchErr := c.repo.GetWalletByUserID(ctx, cmd.UserID)
		if fetchErr != nil {
			logger.Error("failed to fetch wallet after duplicate credit", "error", fetchErr)
			return fmt.Errorf("fetch wallet after duplicate: %w", fetchErr)
		}
		logger.Info("duplicate credit request, returning idempotent success")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyWalletCredited,
			env.CorrelationID,
			messaging.WalletCredited{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				Amount:        cmd.Amount,
				BalanceAfter:  wallet.Balance,
				SourceStep:    cmd.SourceStep,
			},
		)
	}
	if err != nil {
		logger.Error("failed to credit wallet", "error", err)
		return fmt.Errorf("credit wallet: %w", err)
	}

	logger.Info("wallet credited successfully", "balance_after", result.Wallet.Balance)
	return c.publisher.Publish(ctx,
		messaging.ExchangeOutcomes,
		messaging.RoutingKeyWalletCredited,
		env.CorrelationID,
		messaging.WalletCredited{
			TransactionID: cmd.TransactionID,
			UserID:        cmd.UserID,
			Amount:        cmd.Amount,
			BalanceAfter:  result.Wallet.Balance,
			SourceStep:    cmd.SourceStep,
		},
	)
}
