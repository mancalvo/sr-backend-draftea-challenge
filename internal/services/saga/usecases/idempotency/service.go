package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
)

// ErrMismatch indicates the same idempotency key was reused for a different request.
var ErrMismatch = errors.New("idempotency key already used with a different request payload")

// Repository defines the idempotency persistence needed by the service.
type Repository interface {
	GetIdempotencyKey(ctx context.Context, scope, key string) (*domain.IdempotencyKey, error)
	SaveIdempotencyKey(ctx context.Context, key *domain.IdempotencyKey) error
}

// Replay is a previously persisted response to be replayed verbatim.
type Replay struct {
	StatusCode int
	Body       json.RawMessage
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
func (s *Service) Check(ctx context.Context, scope, key, requestHash string) (*Replay, error) {
	cached, err := s.repo.GetIdempotencyKey(ctx, scope, key)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check idempotency key: %w", err)
	}
	if cached.RequestHash != requestHash {
		return nil, ErrMismatch
	}
	return &Replay{
		StatusCode: cached.ResponseStatus,
		Body:       cached.ResponseBody,
	}, nil
}

// SaveAccepted persists the accepted response so retries replay the same result.
func (s *Service) SaveAccepted(ctx context.Context, scope, key, requestHash, transactionID string, status int, response any) error {
	respBody, err := json.Marshal(httpx.Response{
		Success: true,
		Data:    response,
	})
	if err != nil {
		return fmt.Errorf("marshal idempotency response: %w", err)
	}

	ik := &domain.IdempotencyKey{
		Key:            key,
		Scope:          scope,
		RequestHash:    requestHash,
		TransactionID:  transactionID,
		ResponseStatus: status,
		ResponseBody:   respBody,
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}

	if err := s.repo.SaveIdempotencyKey(ctx, ik); err != nil {
		if errors.Is(err, repository.ErrIdempotencyKeyExists) {
			return nil
		}
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}
