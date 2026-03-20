package startpurchase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/commanderror"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/idempotency"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

// Repository defines the saga persistence needed to start a purchase workflow.
type Repository interface {
	CreateSaga(ctx context.Context, saga *domain.SagaInstance) (*domain.SagaInstance, error)
}

// CatalogClient defines the catalog operations needed to start a purchase.
type CatalogClient interface {
	PurchasePrecheck(ctx context.Context, userID, offeringID string) (*client.PrecheckResult, error)
}

// PaymentsClient defines the payments operations needed to start a purchase.
type PaymentsClient interface {
	RegisterTransaction(ctx context.Context, req client.RegisterTransactionRequest) (*client.RegisterTransactionResponse, error)
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

// Command contains the validated start-purchase input.
type Command struct {
	UserID         string
	OfferingID     string
	IdempotencyKey string
}

// UseCase starts purchase sagas.
type UseCase struct {
	repo        Repository
	catalog     CatalogClient
	payments    PaymentsClient
	publisher   Publisher
	idempotency *idempotency.Service
	sagaTimeout time.Duration
}

// New creates a new start-purchase use case.
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

// Execute starts the purchase workflow.
func (u *UseCase) Execute(ctx context.Context, cmd Command) (*Result, error) {
	logger := logging.FromContext(ctx).With(
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	scope := string(domain.SagaTypePurchase)
	requestHash := idempotency.RequestFingerprint(workflows.PurchasePayload{
		UserID:     cmd.UserID,
		OfferingID: cmd.OfferingID,
	})

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	reservation, err := u.idempotency.Reserve(ctx, scope, cmd.IdempotencyKey, requestHash, transactionID)
	if err != nil {
		if errors.Is(err, idempotency.ErrMismatch) {
			return nil, commanderror.New(http.StatusConflict, err.Error(), "IDEMPOTENCY_MISMATCH")
		}
		if errors.Is(err, idempotency.ErrInProgress) {
			return nil, commanderror.New(http.StatusConflict, err.Error(), "IDEMPOTENCY_IN_PROGRESS")
		}
		logger.Error("failed to check idempotency key", "error", err)
		return nil, fmt.Errorf("check idempotency: %w", err)
	}
	if reservation.Replay != nil {
		return &Result{Cached: reservation.Replay}, nil
	}

	precheck, err := u.catalog.PurchasePrecheck(ctx, cmd.UserID, cmd.OfferingID)
	if err != nil {
		logger.Error("purchase precheck failed", "error", err)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusBadGateway, "catalog service unavailable", ""))
	}
	if !precheck.Allowed {
		logger.Info("purchase precheck denied", "reason", precheck.Reason)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusUnprocessableEntity, precheck.Reason, "PRECHECK_DENIED"))
	}
	if precheck.Price <= 0 || precheck.Currency == "" {
		logger.Error("purchase precheck returned invalid pricing", "price", precheck.Price, "currency", precheck.Currency)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusBadGateway, "catalog service unavailable", ""))
	}

	_, err = u.payments.RegisterTransaction(ctx, client.RegisterTransactionRequest{
		ID:         transactionID,
		UserID:     cmd.UserID,
		Type:       "purchase",
		Amount:     precheck.Price,
		Currency:   precheck.Currency,
		OfferingID: &cmd.OfferingID,
	})
	if err != nil {
		logger.Error("failed to register transaction in payments", "error", err)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusBadGateway, "payment service unavailable", ""))
	}

	payload, err := json.Marshal(workflows.PurchasePayload{
		UserID:     cmd.UserID,
		OfferingID: cmd.OfferingID,
		Amount:     precheck.Price,
		Currency:   precheck.Currency,
	})
	if err != nil {
		logger.Error("failed to marshal purchase payload", "error", err)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusInternalServerError, "internal server error", ""))
	}

	timeoutAt := time.Now().UTC().Add(u.sagaTimeout)
	step := workflows.PurchaseDebitStep
	sagaInstance, err := u.repo.CreateSaga(ctx, &domain.SagaInstance{
		TransactionID: transactionID,
		Type:          domain.SagaTypePurchase,
		Status:        domain.StatusRunning,
		CurrentStep:   &step,
		Payload:       payload,
		TimeoutAt:     &timeoutAt,
	})
	if err != nil {
		logger.Error("failed to create saga", "error", err)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusInternalServerError, "internal server error", ""))
	}

	logger.Info("purchase saga created", "saga_id", sagaInstance.ID)

	if err := activities.PublishWalletDebitRequested(ctx, u.publisher, transactionID, messaging.WalletDebitRequested{
		TransactionID: transactionID,
		UserID:        cmd.UserID,
		Amount:        precheck.Price,
		Currency:      precheck.Currency,
		SourceStep:    step,
	}); err != nil {
		logger.Error("failed to publish wallet.debit.requested", "error", err)
		return nil, u.finalizeCommandError(ctx, scope, cmd.IdempotencyKey, commanderror.New(http.StatusBadGateway, "failed to dispatch purchase command", "INITIAL_DISPATCH_FAILED"))
	}

	response := AcceptedResponse{
		TransactionID: transactionID,
		Status:        "pending",
	}
	if err := u.finalizeSuccess(ctx, scope, cmd.IdempotencyKey, response); err != nil {
		logger.Error("failed to finalize idempotency key", "error", err, "key", cmd.IdempotencyKey, "scope", scope)
	}

	return &Result{Response: response}, nil
}

func (u *UseCase) finalizeSuccess(ctx context.Context, scope, key string, response AcceptedResponse) error {
	body, err := httpx.MarshalResponse(http.StatusAccepted, response)
	if err != nil {
		return fmt.Errorf("marshal success response: %w", err)
	}
	return u.idempotency.Finalize(ctx, scope, key, http.StatusAccepted, body)
}

func (u *UseCase) finalizeCommandError(ctx context.Context, scope, key string, err *commanderror.Error) *commanderror.Error {
	body, marshalErr := httpx.MarshalErrorResponse(err.HTTPMessage(), err.HTTPCode(), nil)
	if marshalErr == nil {
		if finalizeErr := u.idempotency.Finalize(ctx, scope, key, err.HTTPStatus(), body); finalizeErr != nil {
			logging.FromContext(ctx).Error("failed to finalize idempotency error response", "error", finalizeErr, "scope", scope, "key", key)
		}
	}
	return err
}
