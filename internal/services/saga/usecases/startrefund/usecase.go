package startrefund

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/commanderror"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/idempotency"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

// Repository defines the saga persistence needed to start a refund workflow.
type Repository interface {
	CreateSaga(ctx context.Context, saga *domain.SagaInstance) (*domain.SagaInstance, error)
}

// CatalogClient defines the catalog operations needed to start a refund.
type CatalogClient interface {
	RefundPrecheck(ctx context.Context, userID, offeringID, transactionID string) (*client.PrecheckResult, error)
}

// PaymentsClient defines the payments operations needed to start a refund.
type PaymentsClient interface {
	RegisterTransaction(ctx context.Context, req client.RegisterTransactionRequest) (*client.RegisterTransactionResponse, error)
	GetTransaction(ctx context.Context, transactionID string) (*client.TransactionDetails, error)
}

// Publisher defines the publishing operations needed to dispatch commands.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// AcceptedResponse is the accepted response returned by the use case.
type AcceptedResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

// Result is the result of executing the use case.
type Result struct {
	Response AcceptedResponse
	Cached   *idempotency.Replay
}

// Command contains the validated start-refund input.
type Command struct {
	UserID         string
	OfferingID     string
	TransactionID  string
	IdempotencyKey string
}

// UseCase starts refund sagas.
type UseCase struct {
	repo        Repository
	catalog     CatalogClient
	payments    PaymentsClient
	publisher   Publisher
	idempotency *idempotency.Service
	sagaTimeout time.Duration
}

// New creates a new start-refund use case.
func New(
	repo Repository,
	catalog CatalogClient,
	payments PaymentsClient,
	publisher Publisher,
	idempotency *idempotency.Service,
	sagaTimeout time.Duration,
) *UseCase {
	return &UseCase{
		repo:        repo,
		catalog:     catalog,
		payments:    payments,
		publisher:   publisher,
		idempotency: idempotency,
		sagaTimeout: sagaTimeout,
	}
}

// Execute starts the refund workflow.
func (u *UseCase) Execute(ctx context.Context, cmd Command) (*Result, error) {
	logger := logging.FromContext(ctx).With(
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
		slog.String("original_transaction_id", cmd.TransactionID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	scope := string(domain.SagaTypeRefund)
	requestHash := idempotency.RequestFingerprint(workflows.RefundPayload{
		UserID:              cmd.UserID,
		OfferingID:          cmd.OfferingID,
		OriginalTransaction: cmd.TransactionID,
	})

	replay, err := u.idempotency.Check(ctx, scope, cmd.IdempotencyKey, requestHash)
	if err != nil {
		if errors.Is(err, idempotency.ErrMismatch) {
			return nil, commanderror.New(http.StatusConflict, err.Error(), "IDEMPOTENCY_MISMATCH")
		}
		logger.Error("failed to check idempotency key", "error", err)
		return nil, fmt.Errorf("check idempotency: %w", err)
	}
	if replay != nil {
		return &Result{Cached: replay}, nil
	}

	precheck, err := u.catalog.RefundPrecheck(ctx, cmd.UserID, cmd.OfferingID, cmd.TransactionID)
	if err != nil {
		logger.Error("refund precheck failed", "error", err)
		return nil, commanderror.New(http.StatusBadGateway, "catalog service unavailable", "")
	}
	if !precheck.Allowed {
		logger.Info("refund precheck denied", "reason", precheck.Reason)
		return nil, commanderror.New(http.StatusUnprocessableEntity, precheck.Reason, "PRECHECK_DENIED")
	}

	originalTxn, err := u.payments.GetTransaction(ctx, cmd.TransactionID)
	if err != nil {
		logger.Error("failed to fetch original transaction", "error", err)
		return nil, commanderror.New(http.StatusBadGateway, "payment service unavailable", "")
	}
	if originalTxn.Type != "purchase" {
		return nil, commanderror.New(http.StatusUnprocessableEntity, "original transaction must be a purchase", "INVALID_ORIGINAL_TRANSACTION")
	}
	if originalTxn.UserID != cmd.UserID {
		return nil, commanderror.New(http.StatusUnprocessableEntity, "original transaction does not belong to user", "INVALID_ORIGINAL_TRANSACTION")
	}
	if originalTxn.OfferingID == nil || *originalTxn.OfferingID != cmd.OfferingID {
		return nil, commanderror.New(http.StatusUnprocessableEntity, "original transaction does not match offering", "INVALID_ORIGINAL_TRANSACTION")
	}
	if originalTxn.Amount <= 0 || originalTxn.Currency == "" {
		return nil, commanderror.New(http.StatusUnprocessableEntity, "original transaction has invalid amount", "INVALID_ORIGINAL_TRANSACTION")
	}

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	_, err = u.payments.RegisterTransaction(ctx, client.RegisterTransactionRequest{
		ID:                    transactionID,
		UserID:                cmd.UserID,
		Type:                  "refund",
		Amount:                originalTxn.Amount,
		Currency:              originalTxn.Currency,
		OfferingID:            &cmd.OfferingID,
		OriginalTransactionID: &cmd.TransactionID,
	})
	if err != nil {
		logger.Error("failed to register transaction in payments", "error", err)
		return nil, commanderror.New(http.StatusBadGateway, "payment service unavailable", "")
	}

	payload, err := json.Marshal(workflows.RefundPayload{
		UserID:              cmd.UserID,
		OfferingID:          cmd.OfferingID,
		OriginalTransaction: cmd.TransactionID,
		Amount:              originalTxn.Amount,
		Currency:            originalTxn.Currency,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal refund payload: %w", err)
	}

	timeoutAt := time.Now().UTC().Add(u.sagaTimeout)
	step := workflows.RefundRevokeAccessStep
	sagaInstance, err := u.repo.CreateSaga(ctx, &domain.SagaInstance{
		TransactionID: transactionID,
		Type:          domain.SagaTypeRefund,
		Status:        domain.StatusRunning,
		CurrentStep:   &step,
		Payload:       payload,
		TimeoutAt:     &timeoutAt,
	})
	if err != nil {
		logger.Error("failed to create saga", "error", err)
		return nil, fmt.Errorf("create saga: %w", err)
	}

	logger.Info("refund saga created", "saga_id", sagaInstance.ID)

	if err := activities.PublishAccessRevokeRequested(ctx, u.publisher, transactionID, messaging.AccessRevokeRequested{
		TransactionID: cmd.TransactionID,
		UserID:        cmd.UserID,
		OfferingID:    cmd.OfferingID,
	}); err != nil {
		logger.Error("failed to publish access.revoke.requested", "error", err)
	}

	response := AcceptedResponse{
		TransactionID: transactionID,
		Status:        "pending",
	}
	if err := u.idempotency.SaveAccepted(ctx, scope, cmd.IdempotencyKey, requestHash, transactionID, http.StatusAccepted, response); err != nil {
		logger.Error("failed to save idempotency key", "error", err, "key", cmd.IdempotencyKey, "scope", scope)
	}

	return &Result{Response: response}, nil
}
