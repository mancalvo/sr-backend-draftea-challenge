package payments

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	paymentsapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/api"
	paymentsconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/consumer"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"
	paymentsprovider "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/provider"
	paymentsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/repository"
	paymentsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/service"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

type (
	ListTransactionsQuery    = domain.ListTransactionsQuery
	Cursor                   = domain.Cursor
	ListTransactionsResult   = domain.ListTransactionsResult
	TransactionType          = domain.TransactionType
	TransactionStatus        = domain.TransactionStatus
	Transaction              = domain.Transaction
	CreateTransactionRequest = paymentsapi.CreateTransactionRequest
	UpdateStatusRequest      = paymentsapi.UpdateStatusRequest
	Repository               = paymentsrepository.Repository
	PostgresRepository       = paymentsrepository.PostgresRepository
	MemoryRepository         = paymentsrepository.MemoryRepository
	Handler                  = paymentsapi.Handler
	ConsumerHandler          = paymentsconsumer.Handler
	ChargeResult             = processdeposit.ChargeResult
	Provider                 = processdeposit.Provider
	Publisher                = processdeposit.Publisher
	MockProvider             = paymentsprovider.MockProvider
)

const (
	DefaultPageLimit = domain.DefaultPageLimit
	MaxPageLimit     = domain.MaxPageLimit

	TransactionTypeDeposit  = domain.TransactionTypeDeposit
	TransactionTypePurchase = domain.TransactionTypePurchase
	TransactionTypeRefund   = domain.TransactionTypeRefund

	StatusPending                = domain.StatusPending
	StatusCompleted              = domain.StatusCompleted
	StatusFailed                 = domain.StatusFailed
	StatusTimedOut               = domain.StatusTimedOut
	StatusCompensated            = domain.StatusCompensated
	StatusReconciliationRequired = domain.StatusReconciliationRequired
)

var (
	ErrInvalidCursor     = domain.ErrInvalidCursor
	ErrNotFound          = paymentsrepository.ErrNotFound
	ErrDuplicateID       = paymentsrepository.ErrDuplicateID
	ErrIllegalTransition = paymentsrepository.ErrIllegalTransition
)

// EncodeCursor serializes a cursor into an opaque string.
func EncodeCursor(c Cursor) string {
	return domain.EncodeCursor(c)
}

// DecodeCursor deserializes an opaque cursor string.
func DecodeCursor(s string) (*Cursor, error) {
	return domain.DecodeCursor(s)
}

// ValidateTransition checks whether a transaction status transition is legal.
func ValidateTransition(from, to TransactionStatus) error {
	return domain.ValidateTransition(from, to)
}

// ValidTransactionType checks whether a transaction type is valid.
func ValidTransactionType(t TransactionType) bool {
	return domain.ValidTransactionType(t)
}

// ValidTransactionStatus checks whether a transaction status is valid.
func ValidTransactionStatus(s TransactionStatus) bool {
	return domain.ValidTransactionStatus(s)
}

// NewPostgresRepository creates a new Postgres repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return paymentsrepository.NewPostgresRepository(db)
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return paymentsrepository.NewMemoryRepository()
}

// NewHandler creates a new payments HTTP handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return paymentsapi.NewHandler(paymentsservice.New(repo), logger)
}

// NewRouter creates the payments HTTP router.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	return paymentsapi.NewRouter(handler, logger, checkers...)
}

// NewConsumerHandler creates a new payments AMQP consumer handler.
func NewConsumerHandler(provider Provider, publisher Publisher, logger *slog.Logger) *ConsumerHandler {
	return paymentsconsumer.NewHandler(processdeposit.New(provider, publisher, logger))
}

// NewMockProvider creates the local mock provider implementation.
func NewMockProvider(delay time.Duration) *MockProvider {
	return paymentsprovider.NewMockProvider(delay)
}

// NewSimulatedProvider preserves the previous constructor name during the refactor.
func NewSimulatedProvider(delay time.Duration) *MockProvider {
	return paymentsprovider.NewMockProvider(delay)
}
