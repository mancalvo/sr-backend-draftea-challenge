package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"time"
)

// ErrMismatch indicates the same idempotency key was reused for a different request.
var ErrMismatch = errors.New("idempotency key already used with a different request payload")

// ErrInProgress indicates the command is already being processed for this key.
var ErrInProgress = errors.New("idempotency key is already being processed")

// Repository defines the idempotency persistence needed by the service.
type Repository interface {
	ReserveIdempotencyKey(ctx context.Context, key *domain.IdempotencyKey, processingTTL time.Duration) (*domain.IdempotencyKey, bool, error)
	FinalizeIdempotencyKey(ctx context.Context, scope, key string, status int, body json.RawMessage, completedTTL time.Duration) error
	MarkIdempotencyKeyRetryable(ctx context.Context, scope, key string) error
}

// Replay is a previously persisted response to be replayed verbatim.
type Replay struct {
	StatusCode int
	Body       json.RawMessage
}

// Reservation is the outcome of trying to claim an idempotency key.
type Reservation struct {
	TransactionID string
	Replay        *Replay
}

// Service manages command idempotency for saga ingress.
type Service struct {
	repo Repository
}

const (
	processingTTL = 30 * time.Second
	completedTTL  = 24 * time.Hour
)

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
		ExpiresAt:     time.Now().UTC().Add(processingTTL),
	}

	cached, reserved, err := s.repo.ReserveIdempotencyKey(ctx, ik, processingTTL)
	if err != nil {
		return nil, fmt.Errorf("reserve idempotency key: %w", err)
	}
	if reserved {
		return &Reservation{TransactionID: cached.TransactionID}, nil
	}
	if cached.RequestHash != requestHash {
		return nil, ErrMismatch
	}
	if cached.State == domain.IdempotencyStateProcessing {
		return nil, ErrInProgress
	}
	return &Reservation{
		TransactionID: cached.TransactionID,
		Replay: &Replay{
			StatusCode: cached.ResponseStatus,
			Body:       cached.ResponseBody,
		},
	}, nil
}

// Finalize stores the final response for a reserved key so retries replay it.
func (s *Service) Finalize(ctx context.Context, scope, key string, status int, body []byte) error {
	if err := s.repo.FinalizeIdempotencyKey(ctx, scope, key, status, json.RawMessage(body), completedTTL); err != nil {
		return fmt.Errorf("finalize idempotency key: %w", err)
	}
	return nil
}

// MarkRetryable releases an in-progress reservation so a retry can reclaim it.
func (s *Service) MarkRetryable(ctx context.Context, scope, key string) error {
	if err := s.repo.MarkIdempotencyKeyRetryable(ctx, scope, key); err != nil {
		return fmt.Errorf("mark idempotency key retryable: %w", err)
	}
	return nil
}
