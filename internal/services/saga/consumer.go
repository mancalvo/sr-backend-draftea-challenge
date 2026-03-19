package saga

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// ConsumerHandler dispatches incoming outcome events from the saga.outcomes
// queue to the appropriate saga workflow handler.
type ConsumerHandler struct {
	repo           Repository
	paymentsClient PaymentsClient
	publisher      Publisher
	logger         *slog.Logger
}

// NewConsumerHandler creates a new ConsumerHandler.
func NewConsumerHandler(
	repo Repository,
	paymentsClient PaymentsClient,
	publisher Publisher,
	logger *slog.Logger,
) *ConsumerHandler {
	return &ConsumerHandler{
		repo:           repo,
		paymentsClient: paymentsClient,
		publisher:      publisher,
		logger:         logger,
	}
}

// HandleOutcome routes an incoming outcome event to the appropriate handler
// based on the event type. This is the main dispatch function used by the
// consumer loop.
func (c *ConsumerHandler) HandleOutcome(ctx context.Context, env messaging.Envelope) error {
	logger := logging.With(c.logger,
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String("event_type", env.Type),
	)

	switch env.Type {
	case messaging.RoutingKeyWalletDebited:
		return c.handleWalletDebited(ctx, env, logger)
	case messaging.RoutingKeyWalletDebitRejected:
		return c.handleWalletDebitRejected(ctx, env, logger)
	case messaging.RoutingKeyWalletCredited:
		return c.handleWalletCredited(ctx, env, logger)
	case messaging.RoutingKeyAccessGranted:
		return c.handleAccessGranted(ctx, env, logger)
	case messaging.RoutingKeyAccessGrantConflicted:
		return c.handleAccessGrantConflicted(ctx, env, logger)
	case messaging.RoutingKeyAccessRevoked:
		return c.handleAccessRevoked(ctx, env, logger)
	case messaging.RoutingKeyAccessRevokeRejected:
		return c.handleAccessRevokeRejected(ctx, env, logger)
	case messaging.RoutingKeyProviderChargeSucceeded:
		return c.handleProviderChargeSucceeded(ctx, env, logger)
	case messaging.RoutingKeyProviderChargeFailed:
		return c.handleProviderChargeFailed(ctx, env, logger)
	default:
		logger.Warn("unknown outcome event type, ignoring")
		return nil
	}
}

// lookupSaga is a helper to find the saga instance for a given transaction ID.
func (c *ConsumerHandler) lookupSaga(ctx context.Context, transactionID string, logger *slog.Logger) (*SagaInstance, error) {
	saga, err := c.repo.GetSagaByTransactionID(ctx, transactionID)
	if err != nil {
		logger.Error("failed to lookup saga by transaction_id", "error", err, logging.KeyTransactionID, transactionID)
		return nil, fmt.Errorf("lookup saga for transaction %s: %w", transactionID, err)
	}
	return saga, nil
}

// ---- Individual outcome handlers ----
// These are stubs that will be fully implemented in T09/T10/T11 (workflow tasks).
// For T08 they establish the routing structure and perform saga lookup.

func (c *ConsumerHandler) handleWalletDebited(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletDebited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debited payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.debited", "saga_id", saga.ID, "saga_status", string(saga.Status))
	// Workflow progression will be implemented in T09/T10/T11.
	return nil
}

func (c *ConsumerHandler) handleWalletDebitRejected(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletDebitRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debit.rejected payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.debit.rejected", "saga_id", saga.ID, "reason", payload.Reason)
	return nil
}

func (c *ConsumerHandler) handleWalletCredited(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletCredited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.credited payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.credited", "saga_id", saga.ID, "saga_status", string(saga.Status))
	return nil
}

func (c *ConsumerHandler) handleAccessGranted(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessGranted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.granted payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.granted", "saga_id", saga.ID, "saga_status", string(saga.Status))
	return nil
}

func (c *ConsumerHandler) handleAccessGrantConflicted(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessGrantConflicted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.grant.conflicted payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.grant.conflicted", "saga_id", saga.ID, "reason", payload.Reason)
	return nil
}

func (c *ConsumerHandler) handleAccessRevoked(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessRevoked
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.revoked payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.revoked", "saga_id", saga.ID, "saga_status", string(saga.Status))
	return nil
}

func (c *ConsumerHandler) handleAccessRevokeRejected(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessRevokeRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.revoke.rejected payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.revoke.rejected", "saga_id", saga.ID, "reason", payload.Reason)
	return nil
}

func (c *ConsumerHandler) handleProviderChargeSucceeded(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.ProviderChargeSucceeded
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode provider.charge.succeeded payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received provider.charge.succeeded", "saga_id", saga.ID, "provider_ref", payload.ProviderRef)
	return nil
}

func (c *ConsumerHandler) handleProviderChargeFailed(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.ProviderChargeFailed
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode provider.charge.failed payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	saga, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received provider.charge.failed", "saga_id", saga.ID, "reason", payload.Reason)
	return nil
}
