package saga

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors returned by repository operations.
var (
	ErrNotFound             = errors.New("not found")
	ErrDuplicateTransaction = errors.New("saga for this transaction already exists")
	ErrIllegalTransition    = errors.New("illegal saga state transition")
	ErrIdempotencyKeyExists = errors.New("idempotency key already exists")
	ErrIdempotencyMismatch  = errors.New("idempotency key already used with a different request payload")
)

// Repository defines the persistence operations for the saga-orchestrator domain.
type Repository interface {
	// CreateSaga inserts a new saga instance. Returns ErrDuplicateTransaction if
	// a saga with the same transaction_id already exists.
	CreateSaga(ctx context.Context, saga *SagaInstance) (*SagaInstance, error)

	// GetSagaByID returns a saga by its primary key, or ErrNotFound.
	GetSagaByID(ctx context.Context, id string) (*SagaInstance, error)

	// GetSagaByTransactionID returns a saga by its transaction_id, or ErrNotFound.
	GetSagaByTransactionID(ctx context.Context, transactionID string) (*SagaInstance, error)

	// UpdateSagaStatus transitions a saga to a new status, validating the transition.
	// Returns ErrNotFound or ErrIllegalTransition on failure.
	UpdateSagaStatus(ctx context.Context, id string, status SagaStatus, outcome *SagaOutcome, currentStep *string) (*SagaInstance, error)

	// ListTimedOutSagas returns sagas whose timeout_at has passed and are still
	// actively executing.
	ListTimedOutSagas(ctx context.Context, now time.Time) ([]SagaInstance, error)

	// SaveIdempotencyKey stores a key-response pair. Returns ErrIdempotencyKeyExists
	// if the key already exists.
	SaveIdempotencyKey(ctx context.Context, key *IdempotencyKey) error

	// GetIdempotencyKey returns a stored key if it exists and has not expired,
	// or ErrNotFound. The lookup is scoped by (scope, key).
	GetIdempotencyKey(ctx context.Context, scope, key string) (*IdempotencyKey, error)
}

// ---- PostgresRepository ----

// PostgresRepository implements Repository against the saga_orchestrator PostgreSQL schema.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateSaga(ctx context.Context, saga *SagaInstance) (*SagaInstance, error) {
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
		if isDuplicateKeyError(err) {
			return nil, ErrDuplicateTransaction
		}
		return nil, fmt.Errorf("create saga: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) GetSagaByID(ctx context.Context, id string) (*SagaInstance, error) {
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

func (r *PostgresRepository) GetSagaByTransactionID(ctx context.Context, transactionID string) (*SagaInstance, error) {
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

func (r *PostgresRepository) UpdateSagaStatus(ctx context.Context, id string, status SagaStatus, outcome *SagaOutcome, currentStep *string) (*SagaInstance, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read current status with row lock.
	const selectQ = `SELECT status FROM saga_orchestrator.saga_instances WHERE id = $1 FOR UPDATE`
	var currentStatus SagaStatus
	if err := tx.QueryRowContext(ctx, selectQ, id).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read saga status: %w", err)
	}

	if err := ValidateSagaTransition(currentStatus, status); err != nil {
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

func (r *PostgresRepository) ListTimedOutSagas(ctx context.Context, now time.Time) ([]SagaInstance, error) {
	const q = `SELECT id, transaction_id, type, status, outcome, current_step, payload, timeout_at, created_at, updated_at
		FROM saga_orchestrator.saga_instances
		WHERE timeout_at <= $1 AND status = 'running'
		ORDER BY timeout_at ASC`

	rows, err := r.db.QueryContext(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("list timed out sagas: %w", err)
	}
	defer rows.Close()

	var result []SagaInstance
	for rows.Next() {
		var s SagaInstance
		var outcome sql.NullString
		var currentStep sql.NullString
		var timeoutAt sql.NullTime
		if err := rows.Scan(
			&s.ID, &s.TransactionID, &s.Type, &s.Status,
			&outcome, &currentStep, &s.Payload, &timeoutAt,
			&s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan saga: %w", err)
		}
		if outcome.Valid {
			o := SagaOutcome(outcome.String)
			s.Outcome = &o
		}
		if currentStep.Valid {
			s.CurrentStep = &currentStep.String
		}
		if timeoutAt.Valid {
			s.TimeoutAt = &timeoutAt.Time
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("saga rows: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) SaveIdempotencyKey(ctx context.Context, key *IdempotencyKey) error {
	const q = `INSERT INTO saga_orchestrator.idempotency_keys
		(scope, key, request_hash, transaction_id, response_status, response_body, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.ExecContext(ctx, q,
		key.Scope, key.Key, key.RequestHash,
		key.TransactionID, key.ResponseStatus, key.ResponseBody, key.ExpiresAt,
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrIdempotencyKeyExists
		}
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetIdempotencyKey(ctx context.Context, scope, key string) (*IdempotencyKey, error) {
	const q = `SELECT scope, key, request_hash, transaction_id, response_status, response_body, created_at, expires_at
		FROM saga_orchestrator.idempotency_keys
		WHERE scope = $1 AND key = $2 AND expires_at > now()`
	var ik IdempotencyKey
	var body sql.NullString
	err := r.db.QueryRowContext(ctx, q, scope, key).Scan(
		&ik.Scope, &ik.Key, &ik.RequestHash,
		&ik.TransactionID, &ik.ResponseStatus, &body,
		&ik.CreatedAt, &ik.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get idempotency key: %w", err)
	}
	if body.Valid {
		ik.ResponseBody = json.RawMessage(body.String)
	}
	return &ik, nil
}

func scanSaga(row *sql.Row) (*SagaInstance, error) {
	var s SagaInstance
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
		o := SagaOutcome(outcome.String)
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

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return containsStr(s, "23505") || containsStr(s, "unique_violation") || containsStr(s, "duplicate key")
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---- In-memory repository for testing ----

// MemoryRepository is an in-memory implementation of Repository for unit tests.
type MemoryRepository struct {
	mu              sync.RWMutex
	sagas           map[string]*SagaInstance
	sagasByTxnID    map[string]string // transaction_id -> saga ID
	idempotencyKeys map[string]*IdempotencyKey
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sagas:           make(map[string]*SagaInstance),
		sagasByTxnID:    make(map[string]string),
		idempotencyKeys: make(map[string]*IdempotencyKey),
	}
}

func (m *MemoryRepository) CreateSaga(_ context.Context, saga *SagaInstance) (*SagaInstance, error) {
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
		stored.Status = StatusCreated
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

func (m *MemoryRepository) GetSagaByID(_ context.Context, id string) (*SagaInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sagas[id]
	if !ok {
		return nil, ErrNotFound
	}
	result := *s
	return &result, nil
}

func (m *MemoryRepository) GetSagaByTransactionID(_ context.Context, transactionID string) (*SagaInstance, error) {
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

func (m *MemoryRepository) UpdateSagaStatus(_ context.Context, id string, status SagaStatus, outcome *SagaOutcome, currentStep *string) (*SagaInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sagas[id]
	if !ok {
		return nil, ErrNotFound
	}

	if err := ValidateSagaTransition(s.Status, status); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
	}

	s.Status = status
	s.Outcome = outcome
	s.CurrentStep = currentStep
	s.UpdatedAt = time.Now().UTC()

	result := *s
	return &result, nil
}

func (m *MemoryRepository) ListTimedOutSagas(_ context.Context, now time.Time) ([]SagaInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []SagaInstance
	for _, s := range m.sagas {
		if s.TimeoutAt != nil && !s.TimeoutAt.After(now) && s.Status == StatusRunning {
			result = append(result, *s)
		}
	}
	return result, nil
}

func idempotencyMapKey(scope, key string) string {
	return scope + "\x00" + key
}

func (m *MemoryRepository) SaveIdempotencyKey(_ context.Context, key *IdempotencyKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mk := idempotencyMapKey(key.Scope, key.Key)
	if _, exists := m.idempotencyKeys[mk]; exists {
		return ErrIdempotencyKeyExists
	}

	stored := *key
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = time.Now().UTC()
	}
	m.idempotencyKeys[mk] = &stored
	return nil
}

func (m *MemoryRepository) GetIdempotencyKey(_ context.Context, scope, key string) (*IdempotencyKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mk := idempotencyMapKey(scope, key)
	ik, ok := m.idempotencyKeys[mk]
	if !ok {
		return nil, ErrNotFound
	}
	if time.Now().UTC().After(ik.ExpiresAt) {
		return nil, ErrNotFound
	}
	result := *ik
	return &result, nil
}
