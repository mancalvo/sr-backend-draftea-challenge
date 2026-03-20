package handleoutcome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

// Repository defines the saga persistence needed to process outcomes.
type Repository interface {
	GetSagaByTransactionID(ctx context.Context, transactionID string) (*domain.SagaInstance, error)
	UpdateSagaStatus(ctx context.Context, id string, status domain.SagaStatus, outcome *domain.SagaOutcome, currentStep *string) (*domain.SagaInstance, error)
}

// PaymentsClient defines the payments operations needed to process outcomes.
type PaymentsClient interface {
	UpdateTransactionStatus(ctx context.Context, transactionID string, status string, reason *string, providerReference *string) error
}

// Publisher defines the publishing operations needed to dispatch commands.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// UseCase processes saga outcome events.
type UseCase struct {
	repo      Repository
	payments  PaymentsClient
	publisher Publisher
	logger    *slog.Logger
}

// New creates a new handle-outcome use case.
func New(repo Repository, payments PaymentsClient, publisher Publisher, logger *slog.Logger) *UseCase {
	return &UseCase{
		repo:      repo,
		payments:  payments,
		publisher: publisher,
		logger:    logger,
	}
}

// Execute routes an incoming outcome event to the appropriate workflow handler.
func (u *UseCase) Execute(ctx context.Context, env messaging.Envelope) error {
	logger := logging.With(u.logger,
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String("event_type", env.Type),
	)

	switch env.Type {
	case messaging.RoutingKeyWalletDebited:
		return u.handleWalletDebited(ctx, env, logger)
	case messaging.RoutingKeyWalletDebitRejected:
		return u.handleWalletDebitRejected(ctx, env, logger)
	case messaging.RoutingKeyWalletCredited:
		return u.handleWalletCredited(ctx, env, logger)
	case messaging.RoutingKeyAccessGranted:
		return u.handleAccessGranted(ctx, env, logger)
	case messaging.RoutingKeyAccessGrantConflicted:
		return u.handleAccessGrantConflicted(ctx, env, logger)
	case messaging.RoutingKeyAccessRevoked:
		return u.handleAccessRevoked(ctx, env, logger)
	case messaging.RoutingKeyAccessRevokeRejected:
		return u.handleAccessRevokeRejected(ctx, env, logger)
	case messaging.RoutingKeyProviderChargeSucceeded:
		return u.handleProviderChargeSucceeded(ctx, env, logger)
	case messaging.RoutingKeyProviderChargeFailed:
		return u.handleProviderChargeFailed(ctx, env, logger)
	default:
		logger.Warn("unknown outcome event type, ignoring")
		return nil
	}
}

func (u *UseCase) lookupSaga(ctx context.Context, transactionID string, logger *slog.Logger) (*domain.SagaInstance, error) {
	saga, err := u.repo.GetSagaByTransactionID(ctx, transactionID)
	if err != nil {
		logger.Error("failed to lookup saga by transaction_id", "error", err, logging.KeyTransactionID, transactionID)
		return nil, fmt.Errorf("lookup saga for transaction %s: %w", transactionID, err)
	}
	return saga, nil
}

func decodePurchasePayload(s *domain.SagaInstance) (*workflows.PurchasePayload, error) {
	var p workflows.PurchasePayload
	if err := json.Unmarshal(s.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode purchase payload: %w", err)
	}
	return &p, nil
}

func decodeRefundPayload(s *domain.SagaInstance) (*workflows.RefundPayload, error) {
	var p workflows.RefundPayload
	if err := json.Unmarshal(s.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode refund payload: %w", err)
	}
	return &p, nil
}

func decodeDepositPayload(s *domain.SagaInstance) (*workflows.DepositPayload, error) {
	var p workflows.DepositPayload
	if err := json.Unmarshal(s.Payload, &p); err != nil {
		return nil, fmt.Errorf("decode deposit payload: %w", err)
	}
	return &p, nil
}

func isSagaTerminal(s *domain.SagaInstance) bool {
	switch s.Status {
	case domain.StatusCompleted, domain.StatusFailed, domain.StatusReconciliationRequired:
		return true
	}
	return false
}

func (u *UseCase) handleWalletDebited(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletDebited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debited payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.debited", "saga_id", s.ID, "saga_status", string(s.Status))

	if isSagaTerminal(s) || s.Status == domain.StatusCompensating {
		logger.Info("saga already past debit step, ignoring duplicate wallet.debited")
		return nil
	}
	if s.Type != domain.SagaTypePurchase {
		return nil
	}

	purchasePayload, err := decodePurchasePayload(s)
	if err != nil {
		return err
	}

	if err := activities.PublishAccessGrantRequested(ctx, u.publisher, s.TransactionID, messaging.AccessGrantRequested{
		TransactionID: s.TransactionID,
		UserID:        purchasePayload.UserID,
		OfferingID:    purchasePayload.OfferingID,
	}); err != nil {
		return fmt.Errorf("publish access.grant.requested: %w", err)
	}

	logger.Info("published access.grant.requested", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleWalletDebitRejected(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletDebitRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debit.rejected payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.debit.rejected", "saga_id", s.ID, "reason", payload.Reason)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.debit.rejected")
		return nil
	}
	if s.Type != domain.SagaTypePurchase {
		return nil
	}

	reason := fmt.Sprintf("wallet debit rejected: %s", payload.Reason)
	if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "failed", &reason, nil); err != nil {
		logger.Error("failed to update transaction to failed", "error", err)
		return fmt.Errorf("update transaction status to failed: %w", err)
	}

	outcome := domain.OutcomeFailed
	step := workflows.PurchaseDebitRejectedStep
	if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusFailed, &outcome, &step); err != nil {
		if errors.Is(err, repository.ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to failed: %w", err)
	}

	logger.Info("purchase saga failed due to insufficient funds", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleWalletCredited(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.WalletCredited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.credited payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received wallet.credited", "saga_id", s.ID, "saga_status", string(s.Status))

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.credited")
		return nil
	}

	if s.Type == domain.SagaTypePurchase && s.Status == domain.StatusCompensating {
		reason := "access grant conflicted, debit reversed"
		if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "compensated", &reason, nil); err != nil {
			logger.Error("failed to update transaction to compensated", "error", err)
			return fmt.Errorf("update transaction status to compensated: %w", err)
		}

		outcome := domain.OutcomeCompensated
		step := workflows.PurchaseCompensationCreditedStep
		if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusCompleted, &outcome, &step); err != nil {
			if errors.Is(err, repository.ErrIllegalTransition) {
				logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
				return nil
			}
			return fmt.Errorf("update saga to completed (compensated): %w", err)
		}

		logger.Info("purchase saga completed with compensation", "saga_id", s.ID)
		return nil
	}

	if s.Type == domain.SagaTypeDeposit && (s.Status == domain.StatusRunning || s.Status == domain.StatusTimedOut) {
		if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "completed", nil, nil); err != nil {
			logger.Error("failed to update transaction to completed", "error", err)
			return fmt.Errorf("update transaction status to completed: %w", err)
		}

		outcome := domain.OutcomeSucceeded
		step := workflows.DepositCompletedStep
		if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusCompleted, &outcome, &step); err != nil {
			if errors.Is(err, repository.ErrIllegalTransition) {
				logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
				return nil
			}
			return fmt.Errorf("update saga to completed (deposit): %w", err)
		}

		logger.Info("deposit saga completed successfully", "saga_id", s.ID)
		return nil
	}

	if s.Type == domain.SagaTypeRefund && s.Status == domain.StatusRunning {
		if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "completed", nil, nil); err != nil {
			logger.Error("failed to update transaction to completed", "error", err)
			return fmt.Errorf("update transaction status to completed: %w", err)
		}

		outcome := domain.OutcomeSucceeded
		step := workflows.RefundCompletedStep
		if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusCompleted, &outcome, &step); err != nil {
			if errors.Is(err, repository.ErrIllegalTransition) {
				logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
				return nil
			}
			return fmt.Errorf("update saga to completed (refund): %w", err)
		}

		logger.Info("refund saga completed successfully", "saga_id", s.ID)
		return nil
	}

	return nil
}

func (u *UseCase) handleAccessGranted(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessGranted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.granted payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.granted", "saga_id", s.ID, "saga_status", string(s.Status))

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.granted")
		return nil
	}
	if s.Type != domain.SagaTypePurchase {
		return nil
	}

	if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "completed", nil, nil); err != nil {
		logger.Error("failed to update transaction to completed", "error", err)
		return fmt.Errorf("update transaction status to completed: %w", err)
	}

	outcome := domain.OutcomeSucceeded
	step := workflows.PurchaseCompletedStep
	if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusCompleted, &outcome, &step); err != nil {
		if errors.Is(err, repository.ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to completed: %w", err)
	}

	logger.Info("purchase saga completed successfully", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleAccessGrantConflicted(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessGrantConflicted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.grant.conflicted payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.grant.conflicted", "saga_id", s.ID, "reason", payload.Reason)

	if isSagaTerminal(s) || s.Status == domain.StatusCompensating {
		logger.Info("saga already past grant step, ignoring duplicate access.grant.conflicted")
		return nil
	}
	if s.Type != domain.SagaTypePurchase {
		return nil
	}

	purchasePayload, err := decodePurchasePayload(s)
	if err != nil {
		return err
	}

	step := workflows.PurchaseCompensationCreditStep
	if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusCompensating, nil, &step); err != nil {
		if errors.Is(err, repository.ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to compensating: %w", err)
	}

	if err := activities.PublishWalletCreditRequested(ctx, u.publisher, s.TransactionID, messaging.WalletCreditRequested{
		TransactionID: s.TransactionID,
		UserID:        purchasePayload.UserID,
		Amount:        purchasePayload.Amount,
		Currency:      purchasePayload.Currency,
		SourceStep:    "purchase_compensation",
	}); err != nil {
		return fmt.Errorf("publish wallet.credit.requested for compensation: %w", err)
	}

	logger.Info("published wallet.credit.requested for compensation", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleAccessRevoked(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessRevoked
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.revoked payload: %w", err)
	}

	sagaTxnID := env.CorrelationID
	logger = logger.With(
		slog.String(logging.KeyTransactionID, sagaTxnID),
		slog.String("original_transaction_id", payload.TransactionID),
	)

	s, err := u.lookupSaga(ctx, sagaTxnID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.revoked", "saga_id", s.ID, "saga_status", string(s.Status))

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.revoked")
		return nil
	}
	if s.Type != domain.SagaTypeRefund {
		return nil
	}

	refundPayload, err := decodeRefundPayload(s)
	if err != nil {
		return err
	}

	if err := activities.PublishWalletCreditRequested(ctx, u.publisher, s.TransactionID, messaging.WalletCreditRequested{
		TransactionID: s.TransactionID,
		UserID:        refundPayload.UserID,
		Amount:        refundPayload.Amount,
		Currency:      refundPayload.Currency,
		SourceStep:    workflows.RefundCreditStep,
	}); err != nil {
		return fmt.Errorf("publish wallet.credit.requested for refund: %w", err)
	}

	logger.Info("published wallet.credit.requested for refund", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleAccessRevokeRejected(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.AccessRevokeRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.revoke.rejected payload: %w", err)
	}

	sagaTxnID := env.CorrelationID
	logger = logger.With(
		slog.String(logging.KeyTransactionID, sagaTxnID),
		slog.String("original_transaction_id", payload.TransactionID),
	)

	s, err := u.lookupSaga(ctx, sagaTxnID, logger)
	if err != nil {
		return err
	}

	logger.Info("received access.revoke.rejected", "saga_id", s.ID, "reason", payload.Reason)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.revoke.rejected")
		return nil
	}
	if s.Type != domain.SagaTypeRefund {
		return nil
	}

	reason := fmt.Sprintf("access revoke rejected: %s", payload.Reason)
	if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "failed", &reason, nil); err != nil {
		logger.Error("failed to update transaction to failed", "error", err)
		return fmt.Errorf("update transaction status to failed: %w", err)
	}

	outcome := domain.OutcomeFailed
	step := workflows.RefundRevokeRejectedStep
	if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusFailed, &outcome, &step); err != nil {
		if errors.Is(err, repository.ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to failed: %w", err)
	}

	logger.Info("refund saga failed due to revoke rejection", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleProviderChargeSucceeded(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.ProviderChargeSucceeded
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode provider.charge.succeeded payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received provider.charge.succeeded", "saga_id", s.ID, "saga_status", string(s.Status), "provider_ref", payload.ProviderRef)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate provider.charge.succeeded")
		return nil
	}
	if s.Type != domain.SagaTypeDeposit {
		return nil
	}

	depositPayload, err := decodeDepositPayload(s)
	if err != nil {
		return err
	}

	transactionStatus := "pending"
	if s.Status == domain.StatusTimedOut {
		transactionStatus = "timed_out"
	}
	if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, transactionStatus, nil, &payload.ProviderRef); err != nil {
		logger.Error("failed to record provider reference", "error", err)
		return fmt.Errorf("record provider reference: %w", err)
	}

	step := workflows.DepositCreditStep
	if s.Status == domain.StatusTimedOut {
		logger.Info("late provider success on timed_out saga, publishing wallet credit", "saga_id", s.ID)
	}

	if err := activities.PublishWalletCreditRequested(ctx, u.publisher, s.TransactionID, messaging.WalletCreditRequested{
		TransactionID: s.TransactionID,
		UserID:        depositPayload.UserID,
		Amount:        depositPayload.Amount,
		Currency:      depositPayload.Currency,
		SourceStep:    step,
	}); err != nil {
		return fmt.Errorf("publish wallet.credit.requested for deposit: %w", err)
	}

	logger.Info("published wallet.credit.requested for deposit", "saga_id", s.ID)
	return nil
}

func (u *UseCase) handleProviderChargeFailed(ctx context.Context, env messaging.Envelope, logger *slog.Logger) error {
	var payload messaging.ProviderChargeFailed
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode provider.charge.failed payload: %w", err)
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, payload.TransactionID))

	s, err := u.lookupSaga(ctx, payload.TransactionID, logger)
	if err != nil {
		return err
	}

	logger.Info("received provider.charge.failed", "saga_id", s.ID, "saga_status", string(s.Status), "reason", payload.Reason)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate provider.charge.failed")
		return nil
	}
	if s.Type != domain.SagaTypeDeposit {
		return nil
	}

	reason := fmt.Sprintf("provider charge failed: %s", payload.Reason)
	if err := u.payments.UpdateTransactionStatus(ctx, s.TransactionID, "failed", &reason, nil); err != nil {
		logger.Error("failed to update transaction to failed", "error", err)
		return fmt.Errorf("update transaction status to failed: %w", err)
	}

	outcome := domain.OutcomeFailed
	step := workflows.DepositChargeFailedStep
	if _, err := u.repo.UpdateSagaStatus(ctx, s.ID, domain.StatusFailed, &outcome, &step); err != nil {
		if errors.Is(err, repository.ErrIllegalTransition) {
			logger.Warn("saga transition rejected, ignoring duplicate", "error", err)
			return nil
		}
		return fmt.Errorf("update saga to failed: %w", err)
	}

	logger.Info("deposit saga failed due to provider charge failure", "saga_id", s.ID)
	return nil
}
