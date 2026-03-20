package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/service"
)

// Publisher defines the subset of publishing operations needed by consumers.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// Handler provides AMQP message handlers for wallets commands.
type Handler struct {
	service   *service.Service
	publisher Publisher
	logger    *slog.Logger
}

// NewHandler creates a new consumer handler.
func NewHandler(service *service.Service, publisher Publisher, logger *slog.Logger) *Handler {
	return &Handler{service: service, publisher: publisher, logger: logger}
}

// Handle routes an incoming wallets command to the matching handler.
func (h *Handler) Handle(ctx context.Context, env messaging.Envelope) error {
	switch env.Type {
	case messaging.RoutingKeyWalletDebitRequested:
		return h.HandleWalletDebitRequested(ctx, env)
	case messaging.RoutingKeyWalletCreditRequested:
		return h.HandleWalletCreditRequested(ctx, env)
	default:
		h.logger.Warn("unknown message type", slog.String("type", env.Type))
		return nil
	}
}

// HandleWalletDebitRequested processes a wallet.debit.requested command.
func (h *Handler) HandleWalletDebitRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.WalletDebitRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode wallet.debit.requested: %w", err)
	}

	logger := h.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.Int64("amount", cmd.Amount),
		slog.String("currency", cmd.Currency),
		slog.String("source_step", cmd.SourceStep),
	)

	logger.Info("processing wallet debit request")

	result, err := h.service.Debit(ctx, cmd.UserID, cmd.TransactionID, cmd.SourceStep, cmd.Amount)
	if errors.Is(err, repository.ErrInsufficientFunds) {
		logger.Warn("wallet debit rejected: insufficient funds")
		return h.publisher.Publish(ctx,
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
	if errors.Is(err, repository.ErrWalletNotFound) {
		logger.Warn("wallet debit rejected: wallet not found")
		return h.publisher.Publish(ctx,
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
	if errors.Is(err, repository.ErrDuplicateMovement) {
		wallet, fetchErr := h.service.GetBalance(ctx, cmd.UserID)
		if fetchErr != nil {
			logger.Error("failed to fetch wallet after duplicate debit", "error", fetchErr)
			return fmt.Errorf("fetch wallet after duplicate: %w", fetchErr)
		}
		logger.Info("duplicate debit request, returning idempotent success")
		return h.publisher.Publish(ctx,
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
	return h.publisher.Publish(ctx,
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
func (h *Handler) HandleWalletCreditRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.WalletCreditRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode wallet.credit.requested: %w", err)
	}

	logger := h.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.Int64("amount", cmd.Amount),
		slog.String("currency", cmd.Currency),
		slog.String("source_step", cmd.SourceStep),
	)

	logger.Info("processing wallet credit request")

	result, err := h.service.Credit(ctx, cmd.UserID, cmd.TransactionID, cmd.SourceStep, cmd.Amount)
	if errors.Is(err, repository.ErrWalletNotFound) {
		logger.Error("wallet credit failed: wallet not found")
		return fmt.Errorf("credit wallet: %w", err)
	}
	if errors.Is(err, repository.ErrDuplicateMovement) {
		wallet, fetchErr := h.service.GetBalance(ctx, cmd.UserID)
		if fetchErr != nil {
			logger.Error("failed to fetch wallet after duplicate credit", "error", fetchErr)
			return fmt.Errorf("fetch wallet after duplicate: %w", fetchErr)
		}
		logger.Info("duplicate credit request, returning idempotent success")
		return h.publisher.Publish(ctx,
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
	return h.publisher.Publish(ctx,
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
