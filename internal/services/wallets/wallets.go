package wallets

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	walletsapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/api"
	walletsconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/consumer"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/domain"
	walletsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/repository"
	walletsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/service"
)

type (
	MovementType       = domain.MovementType
	Wallet             = domain.Wallet
	WalletMovement     = domain.WalletMovement
	Repository         = walletsrepository.Repository
	DebitResult        = walletsrepository.DebitResult
	CreditResult       = walletsrepository.CreditResult
	PostgresRepository = walletsrepository.PostgresRepository
	MemoryRepository   = walletsrepository.MemoryRepository
	Handler            = walletsapi.Handler
	BalanceResponse    = walletsapi.BalanceResponse
	ConsumerHandler    = walletsconsumer.Handler
	Publisher          = walletsconsumer.Publisher
)

const (
	MovementTypeCredit = domain.MovementTypeCredit
	MovementTypeDebit  = domain.MovementTypeDebit
)

var (
	ErrWalletNotFound    = walletsrepository.ErrWalletNotFound
	ErrInsufficientFunds = walletsrepository.ErrInsufficientFunds
	ErrDuplicateMovement = walletsrepository.ErrDuplicateMovement
)

// NewPostgresRepository creates a new Postgres repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return walletsrepository.NewPostgresRepository(db)
}

// NewMemoryRepository creates a new in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return walletsrepository.NewMemoryRepository()
}

// NewHandler creates a new wallets HTTP handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return walletsapi.NewHandler(walletsservice.New(repo), logger)
}

// NewConsumerHandler creates a new wallets message handler.
func NewConsumerHandler(repo Repository, publisher Publisher, logger *slog.Logger) *ConsumerHandler {
	return walletsconsumer.NewHandler(walletsservice.New(repo), publisher, logger)
}

// NewRouter creates the wallets HTTP router.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	return walletsapi.NewRouter(handler, logger, checkers...)
}
