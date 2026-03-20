package service

import (
	"context"
	"errors"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
)

// PrecheckResult carries the result of a precheck.
type PrecheckResult struct {
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
	Price    int64  `json:"price,omitempty"`
	Currency string `json:"currency,omitempty"`
}

// Repository defines the persistence dependency consumed by the catalog-access service.
type Repository interface {
	GetUserByID(ctx context.Context, userID string) (*domain.User, error)
	GetOfferingByID(ctx context.Context, offeringID string) (*domain.Offering, error)
	GetActiveAccess(ctx context.Context, userID, offeringID string) (*domain.AccessRecord, error)
	GetActiveAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error)
	GetAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error)
	ListEntitlements(ctx context.Context, userID string) ([]domain.Entitlement, error)
	GrantAccess(ctx context.Context, userID, offeringID, transactionID string) (*domain.AccessRecord, error)
	RevokeAccess(ctx context.Context, transactionID string) (*domain.AccessRecord, error)
}

// Service provides reusable catalog-access business operations.
type Service struct {
	repo Repository
}

// New creates a new catalog-access service.
func New(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetEntitlements returns all active entitlements for a user after confirming the user exists.
func (s *Service) GetEntitlements(ctx context.Context, userID string) ([]domain.Entitlement, error) {
	if _, err := s.repo.GetUserByID(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListEntitlements(ctx, userID)
}

// PurchasePrecheck validates that a purchase can proceed.
func (s *Service) PurchasePrecheck(ctx context.Context, userID, offeringID string) (*PrecheckResult, error) {
	if _, err := s.repo.GetUserByID(ctx, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &PrecheckResult{Allowed: false, Reason: "user not found"}, nil
		}
		return nil, err
	}

	offering, err := s.repo.GetOfferingByID(ctx, offeringID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &PrecheckResult{Allowed: false, Reason: "offering not found"}, nil
		}
		return nil, err
	}
	if !offering.Active {
		return &PrecheckResult{Allowed: false, Reason: "offering is not active"}, nil
	}

	if _, err := s.repo.GetActiveAccess(ctx, userID, offeringID); err == nil {
		return &PrecheckResult{Allowed: false, Reason: "user already has active access to this offering"}, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	return &PrecheckResult{
		Allowed:  true,
		Price:    offering.Price,
		Currency: offering.Currency,
	}, nil
}

// RefundPrecheck validates that a refund can proceed.
func (s *Service) RefundPrecheck(ctx context.Context, userID, offeringID, transactionID string) (*PrecheckResult, error) {
	if _, err := s.repo.GetUserByID(ctx, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &PrecheckResult{Allowed: false, Reason: "user not found"}, nil
		}
		return nil, err
	}

	access, err := s.repo.GetActiveAccessByTransaction(ctx, transactionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &PrecheckResult{Allowed: false, Reason: "no active access found for this transaction"}, nil
		}
		return nil, err
	}

	if access.UserID != userID || access.OfferingID != offeringID {
		return &PrecheckResult{Allowed: false, Reason: "access record does not match user and offering"}, nil
	}

	return &PrecheckResult{Allowed: true}, nil
}

// GrantAccess grants access to an offering.
func (s *Service) GrantAccess(ctx context.Context, userID, offeringID, transactionID string) (*domain.AccessRecord, error) {
	return s.repo.GrantAccess(ctx, userID, offeringID, transactionID)
}

// RevokeAccess revokes access linked to a transaction.
func (s *Service) RevokeAccess(ctx context.Context, transactionID string) (*domain.AccessRecord, error) {
	return s.repo.RevokeAccess(ctx, transactionID)
}

// GetActiveAccess returns the active access for a user and offering.
func (s *Service) GetActiveAccess(ctx context.Context, userID, offeringID string) (*domain.AccessRecord, error) {
	return s.repo.GetActiveAccess(ctx, userID, offeringID)
}

// GetAccessByTransaction returns any access record linked to a transaction.
func (s *Service) GetAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error) {
	return s.repo.GetAccessByTransaction(ctx, transactionID)
}
