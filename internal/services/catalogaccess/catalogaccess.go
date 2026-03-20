package catalogaccess

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	catalogapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/api"
	catalogconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/consumer"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
	catalogrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
	catalogservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/service"
)

type (
	AccessStatus            = domain.AccessStatus
	User                    = domain.User
	Offering                = domain.Offering
	AccessRecord            = domain.AccessRecord
	Entitlement             = domain.Entitlement
	PurchasePrecheckRequest = catalogapi.PurchasePrecheckRequest
	RefundPrecheckRequest   = catalogapi.RefundPrecheckRequest
	PrecheckResult          = catalogservice.PrecheckResult
	Repository              = catalogrepository.Repository
	PostgresRepository      = catalogrepository.PostgresRepository
	MemoryRepository        = catalogrepository.MemoryRepository
	Handler                 = catalogapi.Handler
	ConsumerHandler         = catalogconsumer.Handler
	Publisher               = catalogconsumer.Publisher
)

const (
	AccessStatusActive  = domain.AccessStatusActive
	AccessStatusRevoked = domain.AccessStatusRevoked
)

var (
	ErrNotFound        = catalogrepository.ErrNotFound
	ErrDuplicateAccess = catalogrepository.ErrDuplicateAccess
	ErrNoActiveAccess  = catalogrepository.ErrNoActiveAccess
)

// NewPostgresRepository creates a new Postgres repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return catalogrepository.NewPostgresRepository(db)
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return catalogrepository.NewMemoryRepository()
}

// NewHandler creates a new catalog-access HTTP handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return catalogapi.NewHandler(catalogservice.New(repo), logger)
}

// NewConsumerHandler creates a new catalog-access consumer handler.
func NewConsumerHandler(repo Repository, publisher Publisher, logger *slog.Logger) *ConsumerHandler {
	return catalogconsumer.NewHandler(catalogservice.New(repo), publisher, logger)
}

// NewRouter creates the catalog-access HTTP router.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	return catalogapi.NewRouter(handler, logger, checkers...)
}
