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
	UpdateSagaStep(ctx context.Context, id string, currentStep *string) (*domain.SagaInstance, error)
}

// PaymentsClient defines the payments operations needed to process outcomes.
type PaymentsClient interface {
	UpdateTransactionStatus(ctx context.Context, transactionID string, status string, reason *string, providerReference *string) error
}

// Publisher defines the publishing operations needed to dispatch commands.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

type actionHandler func(context.Context, messaging.Envelope, *domain.SagaInstance, *slog.Logger) error

// UseCase processes saga outcome events.
type UseCase struct {
	repo      Repository
	payments  PaymentsClient
	publisher Publisher
	logger    *slog.Logger
	handlers  map[workflows.OutcomeAction]actionHandler
}

// New creates a new handle-outcome use case.
func New(repo Repository, payments PaymentsClient, publisher Publisher, logger *slog.Logger) *UseCase {
	u := &UseCase{
		repo:      repo,
		payments:  payments,
		publisher: publisher,
		logger:    logger,
	}
	u.handlers = map[workflows.OutcomeAction]actionHandler{
		workflows.ActionPublishAccessGrant:        u.publishAccessGrant,
		workflows.ActionFailPurchaseDebit:         u.failPurchaseDebit,
		workflows.ActionCompletePurchase:          u.completePurchase,
		workflows.ActionStartPurchaseCompensation: u.startPurchaseCompensation,
		workflows.ActionCompletePurchaseComp:      u.completePurchaseCompensation,
		workflows.ActionPublishRefundCredit:       u.publishRefundCredit,
		workflows.ActionFailRefundRevoke:          u.failRefundRevoke,
		workflows.ActionCompleteRefund:            u.completeRefund,
		workflows.ActionRecordProviderSuccess:     u.recordProviderSuccess,
		workflows.ActionFailProviderCharge:        u.failProviderCharge,
		workflows.ActionCompleteDeposit:           u.completeDeposit,
	}
	return u
}

// Execute routes an incoming outcome event to the appropriate workflow handler.
func (u *UseCase) Execute(ctx context.Context, env messaging.Envelope) error {
	logger := logging.With(u.logger,
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String("event_type", env.Type),
	)

	transactionID, known, err := workflows.SagaTransactionIDForOutcome(env)
	if err != nil {
		return fmt.Errorf("resolve saga transaction id: %w", err)
	}
	if !known {
		logger.Warn("unknown outcome event type, ignoring")
		return nil
	}
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	s, err := u.lookupSaga(ctx, transactionID, logger)
	if err != nil {
		return err
	}

	action, ok := workflows.ActionFor(s.Type, env.Type)
	if !ok {
		logger.Info("outcome event does not apply to saga type, ignoring",
			slog.String("saga_id", s.ID),
			slog.String("saga_type", string(s.Type)),
			slog.String("saga_status", string(s.Status)),
		)
		return nil
	}

	handler, ok := u.handlers[action]
	if !ok {
		return fmt.Errorf("no outcome handler registered for action %q", action)
	}

	logger = logger.With(
		slog.String("saga_id", s.ID),
		slog.String("saga_type", string(s.Type)),
		slog.String("saga_status", string(s.Status)),
	)
	return handler(ctx, env, s, logger)
}

func (u *UseCase) lookupSaga(ctx context.Context, transactionID string, logger *slog.Logger) (*domain.SagaInstance, error) {
	saga, err := u.repo.GetSagaByTransactionID(ctx, transactionID)
	if err != nil {
		logger.Error("failed to lookup saga by transaction_id", "error", err)
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

func (u *UseCase) updateSagaStep(ctx context.Context, sagaID string, step string) error {
	if _, err := u.repo.UpdateSagaStep(ctx, sagaID, &step); err != nil {
		return fmt.Errorf("update saga step to %s: %w", step, err)
	}
	return nil
}

func (u *UseCase) publishAccessGrant(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.WalletDebited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debited payload: %w", err)
	}

	logger.Info("received wallet.debited")

	if isSagaTerminal(s) || s.Status == domain.StatusCompensating {
		logger.Info("saga already past debit step, ignoring duplicate wallet.debited")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.PurchaseDebitStep {
		logger.Info("wallet.debited does not match current purchase step, ignoring duplicate")
		return nil
	}

	purchasePayload, err := decodePurchasePayload(s)
	if err != nil {
		return err
	}

	if err := u.updateSagaStep(ctx, s.ID, workflows.PurchaseGrantStep); err != nil {
		return err
	}
	if err := activities.PublishAccessGrantRequested(ctx, u.publisher, s.TransactionID, messaging.AccessGrantRequested{
		TransactionID: s.TransactionID,
		UserID:        purchasePayload.UserID,
		OfferingID:    purchasePayload.OfferingID,
	}); err != nil {
		return fmt.Errorf("publish access.grant.requested: %w", err)
	}

	logger.Info("published access.grant.requested")
	return nil
}

func (u *UseCase) failPurchaseDebit(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.WalletDebitRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.debit.rejected payload: %w", err)
	}

	logger.Info("received wallet.debit.rejected", "reason", payload.Reason)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.debit.rejected")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.PurchaseDebitStep {
		logger.Info("wallet.debit.rejected does not match current purchase step, ignoring stale event")
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

	logger.Info("purchase saga failed due to insufficient funds")
	return nil
}

func (u *UseCase) completePurchaseCompensation(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.WalletCredited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.credited payload: %w", err)
	}

	logger.Info("received wallet.credited")

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.credited")
		return nil
	}
	if s.Status != domain.StatusCompensating {
		logger.Info("wallet.credited does not apply to purchase in current status, ignoring")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.PurchaseCompensationCreditStep {
		logger.Info("wallet.credited does not match current purchase compensation step, ignoring stale event")
		return nil
	}

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

	logger.Info("purchase saga completed with compensation")
	return nil
}

func (u *UseCase) completeDeposit(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.WalletCredited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.credited payload: %w", err)
	}

	logger.Info("received wallet.credited")

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.credited")
		return nil
	}
	if s.Status != domain.StatusRunning && s.Status != domain.StatusTimedOut {
		logger.Info("wallet.credited does not apply to deposit in current status, ignoring")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.DepositCreditStep {
		logger.Info("wallet.credited does not match current deposit step, ignoring stale event")
		return nil
	}

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

	logger.Info("deposit saga completed successfully")
	return nil
}

func (u *UseCase) completeRefund(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.WalletCredited
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode wallet.credited payload: %w", err)
	}

	logger.Info("received wallet.credited")

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate wallet.credited")
		return nil
	}
	if s.Status != domain.StatusRunning {
		logger.Info("wallet.credited does not apply to refund in current status, ignoring")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.RefundCreditStep {
		logger.Info("wallet.credited does not match current refund step, ignoring stale event")
		return nil
	}

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

	logger.Info("refund saga completed successfully")
	return nil
}

func (u *UseCase) completePurchase(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.AccessGranted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.granted payload: %w", err)
	}

	logger.Info("received access.granted")

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.granted")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.PurchaseGrantStep {
		logger.Info("access.granted does not match current purchase step, ignoring stale event")
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

	logger.Info("purchase saga completed successfully")
	return nil
}

func (u *UseCase) startPurchaseCompensation(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.AccessGrantConflicted
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.grant.conflicted payload: %w", err)
	}

	logger.Info("received access.grant.conflicted", "reason", payload.Reason)

	if isSagaTerminal(s) || s.Status == domain.StatusCompensating {
		logger.Info("saga already past grant step, ignoring duplicate access.grant.conflicted")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.PurchaseGrantStep {
		logger.Info("access.grant.conflicted does not match current purchase step, ignoring stale event")
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

	logger.Info("published wallet.credit.requested for compensation")
	return nil
}

func (u *UseCase) publishRefundCredit(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.AccessRevoked
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.revoked payload: %w", err)
	}

	logger = logger.With(slog.String("original_transaction_id", payload.OriginalTransactionID))
	logger.Info("received access.revoked")

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.revoked")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.RefundRevokeAccessStep {
		logger.Info("access.revoked does not match current refund step, ignoring duplicate")
		return nil
	}

	refundPayload, err := decodeRefundPayload(s)
	if err != nil {
		return err
	}

	if err := u.updateSagaStep(ctx, s.ID, workflows.RefundCreditStep); err != nil {
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

	logger.Info("published wallet.credit.requested for refund")
	return nil
}

func (u *UseCase) failRefundRevoke(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.AccessRevokeRejected
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode access.revoke.rejected payload: %w", err)
	}

	logger = logger.With(slog.String("original_transaction_id", payload.OriginalTransactionID))
	logger.Info("received access.revoke.rejected", "reason", payload.Reason)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate access.revoke.rejected")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.RefundRevokeAccessStep {
		logger.Info("access.revoke.rejected does not match current refund step, ignoring stale event")
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

	logger.Info("refund saga failed due to revoke rejection")
	return nil
}

func (u *UseCase) recordProviderSuccess(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.ProviderChargeSucceeded
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode provider.charge.succeeded payload: %w", err)
	}

	logger.Info("received provider.charge.succeeded", "provider_ref", payload.ProviderRef)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate provider.charge.succeeded")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.DepositChargeStep {
		logger.Info("provider.charge.succeeded does not match current deposit step, ignoring duplicate")
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
		logger.Info("late provider success on timed_out saga, publishing wallet credit")
	}

	if err := u.updateSagaStep(ctx, s.ID, step); err != nil {
		return err
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

	logger.Info("published wallet.credit.requested for deposit")
	return nil
}

func (u *UseCase) failProviderCharge(ctx context.Context, env messaging.Envelope, s *domain.SagaInstance, logger *slog.Logger) error {
	var payload messaging.ProviderChargeFailed
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("decode provider.charge.failed payload: %w", err)
	}

	logger.Info("received provider.charge.failed", "reason", payload.Reason)

	if isSagaTerminal(s) {
		logger.Info("saga already terminal, ignoring duplicate provider.charge.failed")
		return nil
	}
	if s.CurrentStep == nil || *s.CurrentStep != workflows.DepositChargeStep {
		logger.Info("provider.charge.failed does not match current deposit step, ignoring stale event")
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

	logger.Info("deposit saga failed due to provider charge failure")
	return nil
}
