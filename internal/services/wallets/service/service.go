package service

import (
	"context"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/repository"
)

// Repository defines the persistence dependency consumed by the wallets service.
type Repository interface {
	GetWalletByUserID(ctx context.Context, userID string) (*domain.Wallet, error)
	Debit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*repository.DebitResult, error)
	Credit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*repository.CreditResult, error)
}

// Service provides reusable wallet business operations within the bounded context.
type Service struct {
	repo Repository
}

// New creates a new wallets service.
func New(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetBalance returns the current wallet for a user.
func (s *Service) GetBalance(ctx context.Context, userID string) (*domain.Wallet, error) {
	return s.repo.GetWalletByUserID(ctx, userID)
}

// Debit applies a debit movement to the user's wallet.
func (s *Service) Debit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*repository.DebitResult, error) {
	return s.repo.Debit(ctx, userID, transactionID, sourceStep, amount)
}

// Credit applies a credit movement to the user's wallet.
func (s *Service) Credit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*repository.CreditResult, error) {
	return s.repo.Credit(ctx, userID, transactionID, sourceStep, amount)
}
