package saga

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	sagaapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/api"
	sagaclient "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	sagaconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/consumer"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	sagarepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/idempotency"
	timeoutusecase "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/timeout"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

type (
	SagaType                    = domain.SagaType
	SagaStatus                  = domain.SagaStatus
	SagaOutcome                 = domain.SagaOutcome
	SagaInstance                = domain.SagaInstance
	IdempotencyKey              = domain.IdempotencyKey
	DepositCommand              = sagaapi.DepositCommand
	PurchaseCommand             = sagaapi.PurchaseCommand
	RefundCommand               = sagaapi.RefundCommand
	CommandAcceptedResponse     = sagaapi.CommandAcceptedResponse
	Repository                  = sagarepository.Repository
	PostgresRepository          = sagarepository.PostgresRepository
	MemoryRepository            = sagarepository.MemoryRepository
	Handler                     = sagaapi.Handler
	ConsumerHandler             = sagaconsumer.Handler
	Publisher                   = activities.Publisher
	CatalogClient               = sagaclient.CatalogClient
	PaymentsClient              = sagaclient.PaymentsClient
	PrecheckResult              = sagaclient.PrecheckResult
	RegisterTransactionRequest  = sagaclient.RegisterTransactionRequest
	RegisterTransactionResponse = sagaclient.RegisterTransactionResponse
	TransactionDetails          = sagaclient.TransactionDetails
	HTTPCatalogClient           = sagaclient.HTTPCatalogClient
	HTTPPaymentsClient          = sagaclient.HTTPPaymentsClient
	TimeoutConfig               = timeoutusecase.TimeoutConfig
	DepositPayload              = workflows.DepositPayload
	PurchasePayload             = workflows.PurchasePayload
	RefundPayload               = workflows.RefundPayload
)

// TimeoutPoller preserves the previous top-level type while delegating to the timeout use case.
type TimeoutPoller struct {
	*timeoutusecase.TimeoutPoller
}

const (
	SagaTypeDeposit  = domain.SagaTypeDeposit
	SagaTypePurchase = domain.SagaTypePurchase
	SagaTypeRefund   = domain.SagaTypeRefund

	StatusCreated                = domain.StatusCreated
	StatusRunning                = domain.StatusRunning
	StatusTimedOut               = domain.StatusTimedOut
	StatusCompensating           = domain.StatusCompensating
	StatusCompleted              = domain.StatusCompleted
	StatusFailed                 = domain.StatusFailed
	StatusReconciliationRequired = domain.StatusReconciliationRequired

	OutcomeSucceeded   = domain.OutcomeSucceeded
	OutcomeFailed      = domain.OutcomeFailed
	OutcomeCompensated = domain.OutcomeCompensated

	DefaultSagaTimeout = sagaapi.DefaultSagaTimeout

	DepositChargeStep                = workflows.DepositChargeStep
	DepositCreditStep                = workflows.DepositCreditStep
	DepositCompletedStep             = workflows.DepositCompletedStep
	DepositChargeFailedStep          = workflows.DepositChargeFailedStep
	PurchaseDebitStep                = workflows.PurchaseDebitStep
	PurchaseCompletedStep            = workflows.PurchaseCompletedStep
	PurchaseDebitRejectedStep        = workflows.PurchaseDebitRejectedStep
	PurchaseCompensationCreditStep   = workflows.PurchaseCompensationCreditStep
	PurchaseCompensationCreditedStep = workflows.PurchaseCompensationCreditedStep
	RefundRevokeAccessStep           = workflows.RefundRevokeAccessStep
	RefundCreditStep                 = workflows.RefundCreditStep
	RefundCompletedStep              = workflows.RefundCompletedStep
	RefundRevokeRejectedStep         = workflows.RefundRevokeRejectedStep
)

var (
	ErrNotFound             = sagarepository.ErrNotFound
	ErrDuplicateTransaction = sagarepository.ErrDuplicateTransaction
	ErrIllegalTransition    = sagarepository.ErrIllegalTransition
	ErrIdempotencyKeyExists = sagarepository.ErrIdempotencyKeyExists
	ErrIdempotencyMismatch  = sagarepository.ErrIdempotencyMismatch
)

// ValidSagaType checks whether a string is a valid saga type.
func ValidSagaType(t SagaType) bool {
	return domain.ValidSagaType(t)
}

// ValidSagaStatus checks whether a string is a valid saga status.
func ValidSagaStatus(s SagaStatus) bool {
	return domain.ValidSagaStatus(s)
}

// ValidateSagaTransition checks whether a saga state transition is legal.
func ValidateSagaTransition(from, to SagaStatus) error {
	return domain.ValidateSagaTransition(from, to)
}

// RequestFingerprint computes a stable fingerprint for a command payload.
func RequestFingerprint(cmd any) string {
	return idempotency.RequestFingerprint(cmd)
}

// NewPostgresRepository creates a new Postgres repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return sagarepository.NewPostgresRepository(db)
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return sagarepository.NewMemoryRepository()
}

// NewHTTPCatalogClient creates a new catalog service client.
func NewHTTPCatalogClient(baseURL string, timeout time.Duration) *HTTPCatalogClient {
	return sagaclient.NewHTTPCatalogClient(baseURL, timeout)
}

// NewHTTPPaymentsClient creates a new payments service client.
func NewHTTPPaymentsClient(baseURL string, timeout time.Duration) *HTTPPaymentsClient {
	return sagaclient.NewHTTPPaymentsClient(baseURL, timeout)
}

// NewHandler creates a new saga HTTP handler.
func NewHandler(
	repo Repository,
	catalogClient CatalogClient,
	paymentsClient PaymentsClient,
	publisher Publisher,
	sagaTimeout time.Duration,
	logger *slog.Logger,
) *Handler {
	return sagaapi.NewHandler(repo, catalogClient, paymentsClient, publisher, sagaTimeout, logger)
}

// NewConsumerHandler creates a new saga outcome consumer handler.
func NewConsumerHandler(
	repo Repository,
	paymentsClient PaymentsClient,
	publisher Publisher,
	logger *slog.Logger,
) *ConsumerHandler {
	return sagaconsumer.NewHandler(repo, paymentsClient, publisher, logger)
}

// DefaultTimeoutConfig returns the timeout poller's default settings.
func DefaultTimeoutConfig() TimeoutConfig {
	return timeoutusecase.DefaultTimeoutConfig()
}

// NewTimeoutPoller creates a new timeout poller.
func NewTimeoutPoller(
	repo Repository,
	paymentsClient PaymentsClient,
	config TimeoutConfig,
	logger *slog.Logger,
) *TimeoutPoller {
	return &TimeoutPoller{
		TimeoutPoller: timeoutusecase.NewTimeoutPoller(repo, paymentsClient, config, logger),
	}
}

// NewRouter creates the saga HTTP router.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	return sagaapi.NewRouter(handler, logger, checkers...)
}

func (p *TimeoutPoller) poll(ctx context.Context) {
	p.TimeoutPoller.Poll(ctx)
}
