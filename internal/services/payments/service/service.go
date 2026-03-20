package service

import (
	"context"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"
)

// Repository defines the persistence dependency consumed by the payments service.
type Repository interface {
	CreateTransaction(ctx context.Context, txn *domain.Transaction) (*domain.Transaction, error)
	GetTransactionByID(ctx context.Context, id string) (*domain.Transaction, error)
	ListTransactionsByUserID(ctx context.Context, userID string) ([]domain.Transaction, error)
	ListTransactionsByUserIDPaginated(ctx context.Context, query domain.ListTransactionsQuery) (*domain.ListTransactionsResult, error)
	UpdateTransactionStatus(ctx context.Context, id string, status domain.TransactionStatus, reason *string, providerReference *string) (*domain.Transaction, error)
}

// Service provides reusable payments operations within the bounded context.
type Service struct {
	repo Repository
}

// New creates a new payments service.
func New(repo Repository) *Service {
	return &Service{repo: repo}
}

// RegisterTransaction stores a new transaction.
func (s *Service) RegisterTransaction(ctx context.Context, txn *domain.Transaction) (*domain.Transaction, error) {
	return s.repo.CreateTransaction(ctx, txn)
}

// GetTransaction returns a transaction by ID.
func (s *Service) GetTransaction(ctx context.Context, id string) (*domain.Transaction, error) {
	return s.repo.GetTransactionByID(ctx, id)
}

// ListTransactions returns a paginated transaction list for a user.
func (s *Service) ListTransactions(ctx context.Context, query domain.ListTransactionsQuery) (*domain.ListTransactionsResult, error) {
	return s.repo.ListTransactionsByUserIDPaginated(ctx, query)
}

// UpdateTransactionStatus updates a transaction status and metadata.
func (s *Service) UpdateTransactionStatus(ctx context.Context, id string, status domain.TransactionStatus, reason *string, providerReference *string) (*domain.Transaction, error) {
	return s.repo.UpdateTransactionStatus(ctx, id, status, reason, providerReference)
}
