package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"time"
)

// ErrMismatch indicates the same idempotency key was reused for a different request.
var ErrMismatch = errors.New("idempotency key already used with a different request payload")

// ErrInProgress indicates the command is already being processed for this key.
var ErrInProgress = errors.New("idempotency key is already being processed")

// Repository defines the idempotency persistence needed by the service.
type Repository interface {
	GetIdempotencyKey(ctx context.Context, scope, key string) (*domain.IdempotencyKey, error)
	SaveIdempotencyKey(ctx context.Context, key *domain.IdempotencyKey) error
	FinalizeIdempotencyKey(ctx context.Context, scope, key string, status int, body json.RawMessage) error
}

// Replay is a previously persisted response to be replayed verbatim.
type Replay struct {
	StatusCode int
	Body       json.RawMessage
}

// Reservation is the outcome of trying to claim an idempotency key.
type Reservation struct {
	Replay *Replay
}

// Service manages command idempotency for saga ingress.
type Service struct {
	repo Repository
}

// NewService creates a new idempotency service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Check looks up a previous response for the given scope and key.
func (s *Service) Reserve(ctx context.Context, scope, key, requestHash, transactionID string) (*Reservation, error) {
	ik := &domain.IdempotencyKey{
		Key:           key,
		Scope:         scope,
		RequestHash:   requestHash,
		TransactionID: transactionID,
		State:         domain.IdempotencyStateProcessing,
		ExpiresAt:     time.Now().UTC().Add(24 * time.Hour),
	}

	if err := s.repo.SaveIdempotencyKey(ctx, ik); err == nil {
		return &Reservation{}, nil
	} else if !errors.Is(err, repository.ErrIdempotencyKeyExists) {
		return nil, fmt.Errorf("reserve idempotency key: %w", err)
	}

	cached, err := s.repo.GetIdempotencyKey(ctx, scope, key)
	if err != nil {
		return nil, fmt.Errorf("check idempotency key: %w", err)
	}
	if cached.RequestHash != requestHash {
		return nil, ErrMismatch
	}
	if cached.State == domain.IdempotencyStateProcessing {
		return nil, ErrInProgress
	}
	return &Reservation{
		Replay: &Replay{
			StatusCode: cached.ResponseStatus,
			Body:       cached.ResponseBody,
		},
	}, nil
}

// Finalize stores the final response for a reserved key so retries replay it.
func (s *Service) Finalize(ctx context.Context, scope, key string, status int, body []byte) error {
	if err := s.repo.FinalizeIdempotencyKey(ctx, scope, key, status, json.RawMessage(body)); err != nil {
		return fmt.Errorf("finalize idempotency key: %w", err)
	}
	return nil
}
