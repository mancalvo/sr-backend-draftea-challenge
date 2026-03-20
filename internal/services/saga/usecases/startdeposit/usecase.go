package startdeposit

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

// Repository defines the saga persistence needed to start a deposit workflow.
type Repository interface {
	CreateSaga(ctx context.Context, saga *domain.SagaInstance) (*domain.SagaInstance, error)
}

// PaymentsClient defines the payments operations needed to start a deposit.
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

// Command contains the validated start-deposit input.
type Command struct {
	UserID         string
	Amount         int64
	Currency       string
	IdempotencyKey string
}

// UseCase starts deposit sagas.
type UseCase struct {
	repo        Repository
	payments    PaymentsClient
	publisher   Publisher
	idempotency *idempotency.Service
	sagaTimeout time.Duration
}

// New creates a new start-deposit use case.
func New(
	repo Repository,
	payments PaymentsClient,
	publisher Publisher,
	idempotency *idempotency.Service,
	sagaTimeout time.Duration,
) *UseCase {
	return &UseCase{
		repo:        repo,
		payments:    payments,
		publisher:   publisher,
		idempotency: idempotency,
		sagaTimeout: sagaTimeout,
	}
}

// Execute starts the deposit workflow.
func (u *UseCase) Execute(ctx context.Context, cmd Command) (*Result, error) {
	logger := logging.FromContext(ctx).With(
		slog.String("user_id", cmd.UserID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	scope := string(domain.SagaTypeDeposit)
	requestHash := idempotency.RequestFingerprint(workflows.DepositPayload{
		UserID:   cmd.UserID,
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
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

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	_, err = u.payments.RegisterTransaction(ctx, client.RegisterTransactionRequest{
		ID:       transactionID,
		UserID:   cmd.UserID,
		Type:     "deposit",
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
	})
	if err != nil {
		logger.Error("failed to register transaction in payments", "error", err)
		return nil, commanderror.New(http.StatusBadGateway, "payment service unavailable", "")
	}

	payload, err := json.Marshal(workflows.DepositPayload{
		UserID:   cmd.UserID,
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal deposit payload: %w", err)
	}

	timeoutAt := time.Now().UTC().Add(u.sagaTimeout)
	step := workflows.DepositChargeStep
	sagaInstance, err := u.repo.CreateSaga(ctx, &domain.SagaInstance{
		TransactionID: transactionID,
		Type:          domain.SagaTypeDeposit,
		Status:        domain.StatusRunning,
		CurrentStep:   &step,
		Payload:       payload,
		TimeoutAt:     &timeoutAt,
	})
	if err != nil {
		logger.Error("failed to create saga", "error", err)
		return nil, fmt.Errorf("create saga: %w", err)
	}

	logger.Info("deposit saga created", "saga_id", sagaInstance.ID)

	if err := activities.PublishDepositRequested(ctx, u.publisher, transactionID, messaging.DepositRequested{
		TransactionID: transactionID,
		UserID:        cmd.UserID,
		Amount:        cmd.Amount,
		Currency:      cmd.Currency,
	}); err != nil {
		logger.Error("failed to publish payments.deposit.requested", "error", err)
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
