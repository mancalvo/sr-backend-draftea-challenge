package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	platformdatabase "github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
)

// Sentinel errors returned by repository operations.
var (
	ErrNotFound             = errors.New("not found")
	ErrDuplicateTransaction = errors.New("saga for this transaction already exists")
	ErrIllegalTransition    = errors.New("illegal saga state transition")
)

// Repository defines the persistence operations for the saga-orchestrator domain.
type Repository interface {
	// CreateSaga inserts a new saga instance. Returns ErrDuplicateTransaction if
	// a saga with the same transaction_id already exists.
	CreateSaga(ctx context.Context, saga *domain.SagaInstance) (*domain.SagaInstance, error)

	// GetSagaByID returns a saga by its primary key, or ErrNotFound.
	GetSagaByID(ctx context.Context, id string) (*domain.SagaInstance, error)

	// GetSagaByTransactionID returns a saga by its transaction_id, or ErrNotFound.
	GetSagaByTransactionID(ctx context.Context, transactionID string) (*domain.SagaInstance, error)

	// UpdateSagaStatus transitions a saga to a new status, validating the transition.
	// Returns ErrNotFound or ErrIllegalTransition on failure.
	UpdateSagaStatus(ctx context.Context, id string, status domain.SagaStatus, outcome *domain.SagaOutcome, currentStep *string) (*domain.SagaInstance, error)

	// UpdateSagaStep updates only the current step while preserving the current
	// status and outcome.
	UpdateSagaStep(ctx context.Context, id string, currentStep *string) (*domain.SagaInstance, error)

	// ListTimedOutSagas returns sagas whose timeout_at has passed and are still
	// actively executing.
	ListTimedOutSagas(ctx context.Context, now time.Time) ([]domain.SagaInstance, error)

	// ReserveIdempotencyKey atomically claims an idempotency key for processing.
	// It returns the stored record and whether the caller now owns the reservation.
	ReserveIdempotencyKey(ctx context.Context, key *domain.IdempotencyKey, processingTTL time.Duration) (*domain.IdempotencyKey, bool, error)

	// FinalizeIdempotencyKey stores the final response for an existing reserved key.
	FinalizeIdempotencyKey(ctx context.Context, scope, key string, status int, body json.RawMessage, completedTTL time.Duration) error

	// MarkIdempotencyKeyRetryable expires an in-progress reservation immediately
	// so the next retry can reclaim it.
	MarkIdempotencyKeyRetryable(ctx context.Context, scope, key string) error
}

// ---- PostgresRepository ----

// PostgresRepository implements Repository against the saga_orchestrator PostgreSQL schema.
type PostgresRepository struct {
	db *sql.DB
}

type scanner interface {
	Scan(dest ...any) error
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateSaga(ctx context.Context, saga *domain.SagaInstance) (*domain.SagaInstance, error) {
	if saga.ID == "" {
		saga.ID = uuid.New().String()
	}

	const q = `INSERT INTO saga_orchestrator.saga_instances
		(id, transaction_id, type, status, outcome, current_step, payload, timeout_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at`

	row := r.db.QueryRowContext(ctx, q,
		saga.ID, saga.TransactionID, saga.Type, saga.Status,
		saga.Outcome, saga.CurrentStep, saga.Payload, saga.TimeoutAt,
	)
	result, err := scanSaga(row)
	if err != nil {
		if platformdatabase.IsUniqueViolation(err) {
			return nil, ErrDuplicateTransaction
		}
		return nil, fmt.Errorf("create saga: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) GetSagaByID(ctx context.Context, id string) (*domain.SagaInstance, error) {
	const q = `SELECT id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at
		FROM saga_orchestrator.saga_instances WHERE id = $1`
	s, err := scanSaga(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get saga %s: %w", id, err)
	}
	return s, nil
}

func (r *PostgresRepository) GetSagaByTransactionID(ctx context.Context, transactionID string) (*domain.SagaInstance, error) {
	const q = `SELECT id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at
		FROM saga_orchestrator.saga_instances WHERE transaction_id = $1`
	s, err := scanSaga(r.db.QueryRowContext(ctx, q, transactionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get saga by transaction %s: %w", transactionID, err)
	}
	return s, nil
}

func (r *PostgresRepository) UpdateSagaStatus(ctx context.Context, id string, status domain.SagaStatus, outcome *domain.SagaOutcome, currentStep *string) (*domain.SagaInstance, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read current status with row lock.
	const selectQ = `SELECT status FROM saga_orchestrator.saga_instances WHERE id = $1 FOR UPDATE`
	var currentStatus domain.SagaStatus
	if err := tx.QueryRowContext(ctx, selectQ, id).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read saga status: %w", err)
	}

	if err := domain.ValidateSagaTransition(currentStatus, status); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
	}

	const updateQ = `UPDATE saga_orchestrator.saga_instances
		SET status = $1, outcome = $2, current_step = $3, updated_at = now()
		WHERE id = $4
		RETURNING id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at`

	row := tx.QueryRowContext(ctx, updateQ, status, outcome, currentStep, id)
	result, err := scanSaga(row)
	if err != nil {
		return nil, fmt.Errorf("scan updated saga: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) UpdateSagaStep(ctx context.Context, id string, currentStep *string) (*domain.SagaInstance, error) {
	const q = `UPDATE saga_orchestrator.saga_instances
		SET current_step = $1, updated_at = now()
		WHERE id = $2
		RETURNING id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at`

	row := r.db.QueryRowContext(ctx, q, currentStep, id)
	result, err := scanSaga(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update saga step: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) ListTimedOutSagas(ctx context.Context, now time.Time) ([]domain.SagaInstance, error) {
	const q = `SELECT id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at
		FROM saga_orchestrator.saga_instances
		WHERE timeout_at <= $1 AND status = 'running'
		ORDER BY timeout_at ASC`

	rows, err := r.db.QueryContext(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("list timed out sagas: %w", err)
	}
	defer rows.Close()

	var result []domain.SagaInstance
	for rows.Next() {
		saga, err := scanSaga(rows)
		if err != nil {
			return nil, fmt.Errorf("scan saga: %w", err)
		}
		result = append(result, *saga)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("saga rows: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) ReserveIdempotencyKey(ctx context.Context, key *domain.IdempotencyKey, processingTTL time.Duration) (*domain.IdempotencyKey, bool, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(processingTTL)

	const insertQ = `INSERT INTO saga_orchestrator.idempotency_keys
		(scope, key, request_hash, transaction_id, state, response_status, response_body, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (scope, key) DO NOTHING
		RETURNING scope, key, request_hash, transaction_id, state, response_status, response_body, created_at, expires_at`

	inserted, err := scanIdempotencyKey(r.db.QueryRowContext(ctx, insertQ,
		key.Scope, key.Key, key.RequestHash, key.TransactionID,
		domain.IdempotencyStateProcessing, 0, nil, expiresAt,
	))
	if err == nil {
		return inserted, true, nil
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("insert idempotency key: %w", err)
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	const selectQ = `SELECT scope, key, request_hash, transaction_id, state, response_status, response_body, created_at, expires_at
		FROM saga_orchestrator.idempotency_keys
		WHERE scope = $1 AND key = $2
		FOR UPDATE`

	existing, err := scanIdempotencyKey(tx.QueryRowContext(ctx, selectQ, key.Scope, key.Key))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, fmt.Errorf("select idempotency key: %w", err)
	}

	if existing.ExpiresAt.After(now) {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit tx: %w", err)
		}
		return existing, false, nil
	}

	nextTransactionID := key.TransactionID
	nextRequestHash := key.RequestHash
	if existing.State == domain.IdempotencyStateProcessing && existing.RequestHash == key.RequestHash {
		nextTransactionID = existing.TransactionID
	}

	const updateQ = `UPDATE saga_orchestrator.idempotency_keys
		SET request_hash = $1,
			transaction_id = $2,
			state = 'processing',
			response_status = 0,
			response_body = NULL,
			expires_at = $3
		WHERE scope = $4 AND key = $5`
	if _, err := tx.ExecContext(ctx, updateQ, nextRequestHash, nextTransactionID, expiresAt, key.Scope, key.Key); err != nil {
		return nil, false, fmt.Errorf("reclaim idempotency key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit tx: %w", err)
	}

	existing.RequestHash = nextRequestHash
	existing.TransactionID = nextTransactionID
	existing.State = domain.IdempotencyStateProcessing
	existing.ResponseStatus = 0
	existing.ResponseBody = nil
	existing.ExpiresAt = expiresAt
	return existing, true, nil
}

func (r *PostgresRepository) FinalizeIdempotencyKey(ctx context.Context, scope, key string, status int, body json.RawMessage, completedTTL time.Duration) error {
	const q = `UPDATE saga_orchestrator.idempotency_keys
		SET state = 'completed', response_status = $1, response_body = $2, expires_at = $3
		WHERE scope = $4 AND key = $5`
	result, err := r.db.ExecContext(ctx, q, status, body, time.Now().UTC().Add(completedTTL), scope, key)
	if err != nil {
		return fmt.Errorf("finalize idempotency key: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("idempotency rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) MarkIdempotencyKeyRetryable(ctx context.Context, scope, key string) error {
	const q = `UPDATE saga_orchestrator.idempotency_keys
		SET state = 'processing', response_status = 0, response_body = NULL, expires_at = now()
		WHERE scope = $1 AND key = $2`
	result, err := r.db.ExecContext(ctx, q, scope, key)
	if err != nil {
		return fmt.Errorf("mark idempotency key retryable: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("idempotency rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSaga(row scanner) (*domain.SagaInstance, error) {
	var s domain.SagaInstance
	var outcome sql.NullString
	var currentStep sql.NullString
	var timeoutAt sql.NullTime
	err := row.Scan(
		&s.ID, &s.TransactionID, &s.Type, &s.Status,
		&outcome, &currentStep, &s.Payload, &timeoutAt,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if outcome.Valid {
		o := domain.SagaOutcome(outcome.String)
		s.Outcome = &o
	}
	if currentStep.Valid {
		s.CurrentStep = &currentStep.String
	}
	if timeoutAt.Valid {
		s.TimeoutAt = &timeoutAt.Time
	}
	return &s, nil
}

func scanIdempotencyKey(row scanner) (*domain.IdempotencyKey, error) {
	var ik domain.IdempotencyKey
	var state string
	var body sql.NullString
	err := row.Scan(
		&ik.Scope, &ik.Key, &ik.RequestHash,
		&ik.TransactionID, &state, &ik.ResponseStatus, &body,
		&ik.CreatedAt, &ik.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	if body.Valid {
		ik.ResponseBody = json.RawMessage(body.String)
	}
	ik.State = domain.IdempotencyState(state)
	return &ik, nil
}

// ---- In-memory repository for testing ----

// MemoryRepository is an in-memory implementation of Repository for unit tests.
type MemoryRepository struct {
	mu              sync.RWMutex
	sagas           map[string]*domain.SagaInstance
	sagasByTxnID    map[string]string // transaction_id -> saga ID
	idempotencyKeys map[string]*domain.IdempotencyKey
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sagas:           make(map[string]*domain.SagaInstance),
		sagasByTxnID:    make(map[string]string),
		idempotencyKeys: make(map[string]*domain.IdempotencyKey),
	}
}

func (m *MemoryRepository) CreateSaga(_ context.Context, saga *domain.SagaInstance) (*domain.SagaInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sagasByTxnID[saga.TransactionID]; exists {
		return nil, ErrDuplicateTransaction
	}

	now := time.Now().UTC()
	stored := *saga
	if stored.ID == "" {
		stored.ID = uuid.New().String()
	}
	if stored.Status == "" {
		stored.Status = domain.StatusCreated
	}
	if stored.Payload == nil {
		stored.Payload = json.RawMessage("{}")
	}
	stored.CreatedAt = now
	stored.UpdatedAt = now

	m.sagas[stored.ID] = &stored
	m.sagasByTxnID[stored.TransactionID] = stored.ID

	result := stored
	return &result, nil
}

func (m *MemoryRepository) GetSagaByID(_ context.Context, id string) (*domain.SagaInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sagas[id]
	if !ok {
		return nil, ErrNotFound
	}
	result := *s
	return &result, nil
}

func (m *MemoryRepository) GetSagaByTransactionID(_ context.Context, transactionID string) (*domain.SagaInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sagaID, ok := m.sagasByTxnID[transactionID]
	if !ok {
		return nil, ErrNotFound
	}
	s := m.sagas[sagaID]
	result := *s
	return &result, nil
}

func (m *MemoryRepository) UpdateSagaStatus(_ context.Context, id string, status domain.SagaStatus, outcome *domain.SagaOutcome, currentStep *string) (*domain.SagaInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sagas[id]
	if !ok {
		return nil, ErrNotFound
	}

	if err := domain.ValidateSagaTransition(s.Status, status); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
	}

	s.Status = status
	s.Outcome = outcome
	s.CurrentStep = currentStep
	s.UpdatedAt = time.Now().UTC()

	result := *s
	return &result, nil
}

func (m *MemoryRepository) UpdateSagaStep(_ context.Context, id string, currentStep *string) (*domain.SagaInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sagas[id]
	if !ok {
		return nil, ErrNotFound
	}

	s.CurrentStep = currentStep
	s.UpdatedAt = time.Now().UTC()

	result := *s
	return &result, nil
}

func (m *MemoryRepository) ListTimedOutSagas(_ context.Context, now time.Time) ([]domain.SagaInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []domain.SagaInstance
	for _, s := range m.sagas {
		if s.TimeoutAt != nil && !s.TimeoutAt.After(now) && s.Status == domain.StatusRunning {
			result = append(result, *s)
		}
	}
	return result, nil
}

func idempotencyMapKey(scope, key string) string {
	return scope + "\x00" + key
}

func (m *MemoryRepository) ReserveIdempotencyKey(_ context.Context, key *domain.IdempotencyKey, processingTTL time.Duration) (*domain.IdempotencyKey, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	mk := idempotencyMapKey(key.Scope, key.Key)
	existing, exists := m.idempotencyKeys[mk]
	if !exists {
		stored := *key
		stored.CreatedAt = now
		stored.ExpiresAt = now.Add(processingTTL)
		stored.State = domain.IdempotencyStateProcessing
		stored.ResponseStatus = 0
		stored.ResponseBody = nil
		m.idempotencyKeys[mk] = &stored
		result := stored
		return &result, true, nil
	}

	if existing.ExpiresAt.After(now) {
		result := *existing
		if existing.ResponseBody != nil {
			result.ResponseBody = append(json.RawMessage(nil), existing.ResponseBody...)
		}
		return &result, false, nil
	}

	nextTransactionID := key.TransactionID
	nextRequestHash := key.RequestHash
	if existing.State == domain.IdempotencyStateProcessing && existing.RequestHash == key.RequestHash {
		nextTransactionID = existing.TransactionID
	}

	existing.RequestHash = nextRequestHash
	existing.TransactionID = nextTransactionID
	existing.State = domain.IdempotencyStateProcessing
	existing.ResponseStatus = 0
	existing.ResponseBody = nil
	existing.ExpiresAt = now.Add(processingTTL)

	result := *existing
	return &result, true, nil
}

func (m *MemoryRepository) FinalizeIdempotencyKey(_ context.Context, scope, key string, status int, body json.RawMessage, completedTTL time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mk := idempotencyMapKey(scope, key)
	ik, ok := m.idempotencyKeys[mk]
	if !ok {
		return ErrNotFound
	}
	ik.State = domain.IdempotencyStateCompleted
	ik.ResponseStatus = status
	ik.ResponseBody = append(json.RawMessage(nil), body...)
	ik.ExpiresAt = time.Now().UTC().Add(completedTTL)
	return nil
}

func (m *MemoryRepository) MarkIdempotencyKeyRetryable(_ context.Context, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mk := idempotencyMapKey(scope, key)
	ik, ok := m.idempotencyKeys[mk]
	if !ok {
		return ErrNotFound
	}
	ik.State = domain.IdempotencyStateProcessing
	ik.ResponseStatus = 0
	ik.ResponseBody = nil
	ik.ExpiresAt = time.Now().UTC()
	return nil
}
