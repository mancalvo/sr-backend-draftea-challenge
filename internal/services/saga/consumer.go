package saga

import (
	"context"
	"encoding/json"
	"errors"
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

// decodePurchasePayload extracts PurchasePayload from a saga's stored payload.
func decodePurchasePayload(s *SagaInstance) (*PurchasePayload, error) {
	var p PurchasePayload
	if err := json.Unmarshal(s.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode purchase payload: %w", err)
	}
	return &p, nil
}

// isSagaTerminal returns true if the saga is already in a terminal state
// (completed, failed, reconciliation_required). Used to handle duplicate
// event redelivery gracefully.
func isSagaTerminal(s *SagaInstance) bool {
	switch s.Status {
	case StatusCompleted, StatusFailed, StatusReconciliationRequired:
		return true
	}
	return false
}

func (c *ConsumerHandler) handleWalletDebited(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletDebited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debited payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.debited", "saga_id", s.ID, "saga_status", string(s.Status))

	// Idempotent: if the saga already moved past this step, ignore.
	if isSagaTerminal(s) || s.Status == StatusCompensating {
		logger.Info("saga already past debit step, ignoring duplicate wallet.debited")
		return nil
	}

	// Only purchase sagas proceed with access grant after debit.
	if s.Type != SagaTypePurchase {
		// Other workflow types (deposit) handle this event in their own tasks.
		return nil
	}

	purchasePayload, err := decodePurchasePayload(s)
	if err != nil {
		return err
	}

	// Publish access.grant.requested.
	if err := c.publisher.Publish(ctx,
		messaging.ExchangeCommands,
		messaging.RoutingKeyAccessGrantRequested,
		s.TransactionID,
		messaging.AccessGrantRequested{
			TransactionID: s.TransactionID,
			UserID:        purchasePayload.UserID,
			OfferingID:    purchasePayload.OfferingID,
		},
	); err != nil {
		return fmt.Errorf("publish access.grant.requested: %w", err)
	}

	logger.Info("published access.grant.requested", "saga_id", s.ID)
	return nil
}

func (c *ConsumerHandler) handleWalletDebitRejected(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletDebitRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debit.rejected payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.debit.rejected", "saga_id", s.ID, "reason", payload.Reason)

	// Idempotent: already terminal, nothing to do.
	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.debit.rejected")
		return nil
	}

	if s.Type != SagaTypePurchase {
		return nil
	}

	// Insufficient funds -> fail the saga and the transaction.
	outcome := OutcomeFailed
	step := "purchase_debit_rejected"
	if _, err := c.repo.UpdateSagaStatus(ctx, s.ID, StatusFailed, &outcome, &step); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to failed: %w", err)
	}

	reason := fmt.Sprintf("insufficient funds: %s", payload.Reason)
	if err := c.paymentsClient.UpdateTransactionStatus(ctx, s.TransactionID, "failed", &reason); err != nil {
		logger.Error("failed to update transaction to failed", "error", err)
		return fmt.Errorf("update transaction status to failed: %w", err)
	}

	logger.Info("purchase saga failed due to insufficient funds", "saga_id", s.ID)
	return nil
}

func (c *ConsumerHandler) handleWalletCredited(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletCredited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.credited payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.credited", "saga_id", s.ID, "saga_status", string(s.Status))

	// Idempotent: already terminal, nothing to do.
	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.credited")
		return nil
	}

	// For purchase sagas in compensating status: credit completes the compensation.
	if s.Type == SagaTypePurchase && s.Status == StatusCompensating {
		outcome := OutcomeCompensated
		step := "purchase_compensation_credited"
		if _, err := c.repo.UpdateSagaStatus(ctx, s.ID, StatusCompleted, &outcome, &step); err != nil {
			if errors.Is(err, ErrIllegalTransition) {
				logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
				return nil
			}
			return fmt.Errorf("update saga to completed (compensated): %w", err)
		}

		reason := "access grant conflicted, debit reversed"
		if err := c.paymentsClient.UpdateTransactionStatus(ctx, s.TransactionID, "compensated", &reason); err != nil {
			logger.Error("failed to update transaction to compensated", "error", err)
			return fmt.Errorf("update transaction status to compensated: %w", err)
		}

		logger.Info("purchase saga completed with compensation", "saga_id", s.ID)
		return nil
	}

	// Other saga types (refund) will handle wallet.credited in T10/T11.
	return nil
}

func (c *ConsumerHandler) handleAccessGranted(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessGranted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.granted payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.granted", "saga_id", s.ID, "saga_status", string(s.Status))

	// Idempotent: already terminal, nothing to do.
	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.granted")
		return nil
	}

	if s.Type != SagaTypePurchase {
		return nil
	}

	// Access granted -> purchase completed successfully.
	outcome := OutcomeSucceeded
	step := "purchase_completed"
	if _, err := c.repo.UpdateSagaStatus(ctx, s.ID, StatusCompleted, &outcome, &step); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to completed: %w", err)
	}

	if err := c.paymentsClient.UpdateTransactionStatus(ctx, s.TransactionID, "completed", nil); err != nil {
		logger.Error("failed to update transaction to completed", "error", err)
		return fmt.Errorf("update transaction status to completed: %w", err)
	}

	logger.Info("purchase saga completed successfully", "saga_id", s.ID)
	return nil
}

func (c *ConsumerHandler) handleAccessGrantConflicted(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessGrantConflicted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.grant.conflicted payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := c.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.grant.conflicted", "saga_id", s.ID, "reason", payload.Reason)

	// Idempotent: already terminal or compensating, nothing to do.
	if isSagaTerminal(s) || s.Status == StatusCompensating {
		logger.Info("saga already past grant step, ignoring duplicate access.grant.conflicted")
		return nil
	}

	if s.Type != SagaTypePurchase {
		return nil
	}

	purchasePayload, err := decodePurchasePayload(s)
	if err != nil {
		return err
	}

	// Access grant conflicted after debit -> compensate by crediting wallet.
	step := "purchase_compensation_credit"
	if _, err := c.repo.UpdateSagaStatus(ctx, s.ID, StatusCompensating, nil, &step); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to compensating: %w", err)
	}

	if err := c.publisher.Publish(ctx,
		messaging.ExchangeCommands,
		messaging.RoutingKeyWalletCreditRequested,
		s.TransactionID,
		messaging.WalletCreditRequested{
			TransactionID: s.TransactionID,
			UserID:        purchasePayload.UserID,
			Amount:        purchasePayload.Amount,
			Currency:      purchasePayload.Currency,
			SourceStep:    "purchase_compensation",
		},
	); err != nil {
		return fmt.Errorf("publish wallet.credit.requested for compensation: %w", err)
	}

	logger.Info("published wallet.credit.requested for compensation", "saga_id", s.ID)
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
