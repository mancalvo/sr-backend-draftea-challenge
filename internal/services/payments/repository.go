package payments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Sentinel errors returned by repository operations.
var (
	ErrNotFound          = errors.New("not found")
	ErrDuplicateID       = errors.New("transaction with this ID already exists")
	ErrIllegalTransition = errors.New("illegal state transition")
)

// Repository defines the persistence operations for the payments domain.
type Repository interface {
	// CreateTransaction inserts a new transaction. Returns ErrDuplicateID if the ID already exists.
	CreateTransaction(ctx context.Context, txn *Transaction) (*Transaction, error)

	// GetTransactionByID returns a single transaction by its ID, or ErrNotFound.
	GetTransactionByID(ctx context.Context, id string) (*Transaction, error)

	// ListTransactionsByUserID returns transactions for a user, ordered by created_at DESC.
	ListTransactionsByUserID(ctx context.Context, userID string) ([]Transaction, error)

	// UpdateTransactionStatus transitions a transaction to a new status.
	// It validates the transition legality before applying the change.
	// Returns ErrNotFound if the transaction does not exist, or ErrIllegalTransition
	// if the transition is not allowed.
	UpdateTransactionStatus(ctx context.Context, id string, status TransactionStatus, reason *string) (*Transaction, error)
}

// PostgresRepository implements Repository against the payments PostgreSQL schema.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateTransaction(ctx context.Context, txn *Transaction) (*Transaction, error) {
	const q = `INSERT INTO payments.transactions (id, user_id, type, status, amount, currency, offering_id, status_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, user_id, type, status, amount, currency, offering_id, status_reason, created_at, updated_at`

	row := r.db.QueryRowContext(ctx, q,
		txn.ID, txn.UserID, txn.Type, txn.Status,
		txn.Amount, txn.Currency, txn.OfferingID, txn.StatusReason,
	)
	result, err := scanTransaction(row)
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrDuplicateID
		}
		return nil, fmt.Errorf("create transaction: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) GetTransactionByID(ctx context.Context, id string) (*Transaction, error) {
	const q = `SELECT id, user_id, type, status, amount, currency, offering_id, status_reason, created_at, updated_at
		FROM payments.transactions WHERE id = $1`
	txn, err := scanTransaction(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get transaction %s: %w", id, err)
	}
	return txn, nil
}

func (r *PostgresRepository) ListTransactionsByUserID(ctx context.Context, userID string) ([]Transaction, error) {
	const q = `SELECT id, user_id, type, status, amount, currency, offering_id, status_reason, created_at, updated_at
		FROM payments.transactions
		WHERE user_id = $1
		ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list transactions for user %s: %w", userID, err)
	}
	defer rows.Close()

	var result []Transaction
	for rows.Next() {
		var t Transaction
		var offeringID sql.NullString
		var statusReason sql.NullString
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.Type, &t.Status,
			&t.Amount, &t.Currency, &offeringID, &statusReason,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		if offeringID.Valid {
			t.OfferingID = &offeringID.String
		}
		if statusReason.Valid {
			t.StatusReason = &statusReason.String
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transaction rows: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) UpdateTransactionStatus(ctx context.Context, id string, status TransactionStatus, reason *string) (*Transaction, error) {
	// Use a database transaction to ensure atomicity of read-check-update.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read current status with row lock.
	const selectQ = `SELECT status FROM payments.transactions WHERE id = $1 FOR UPDATE`
	var currentStatus TransactionStatus
	if err := tx.QueryRowContext(ctx, selectQ, id).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read current status: %w", err)
	}

	// Validate the transition.
	if err := ValidateTransition(currentStatus, status); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
	}

	// Apply the update.
	const updateQ = `UPDATE payments.transactions
		SET status = $1, status_reason = $2, updated_at = now()
		WHERE id = $3
		RETURNING id, user_id, type, status, amount, currency, offering_id, status_reason, created_at, updated_at`
	row := tx.QueryRowContext(ctx, updateQ, status, reason, id)

	var t Transaction
	var offeringID sql.NullString
	var statusReason sql.NullString
	if err := row.Scan(
		&t.ID, &t.UserID, &t.Type, &t.Status,
		&t.Amount, &t.Currency, &offeringID, &statusReason,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan updated transaction: %w", err)
	}
	if offeringID.Valid {
		t.OfferingID = &offeringID.String
	}
	if statusReason.Valid {
		t.StatusReason = &statusReason.String
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return &t, nil
}

// scanTransaction scans a single row into a Transaction.
func scanTransaction(row *sql.Row) (*Transaction, error) {
	var t Transaction
	var offeringID sql.NullString
	var statusReason sql.NullString
	err := row.Scan(
		&t.ID, &t.UserID, &t.Type, &t.Status,
		&t.Amount, &t.Currency, &offeringID, &statusReason,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if offeringID.Valid {
		t.OfferingID = &offeringID.String
	}
	if statusReason.Valid {
		t.StatusReason = &statusReason.String
	}
	return &t, nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique_violation (23505).
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
	mu           sync.RWMutex
	transactions map[string]*Transaction
	order        []string // insertion order for listing
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		transactions: make(map[string]*Transaction),
	}
}

func (m *MemoryRepository) CreateTransaction(_ context.Context, txn *Transaction) (*Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.transactions[txn.ID]; exists {
		return nil, ErrDuplicateID
	}

	now := time.Now().UTC()
	stored := *txn
	stored.CreatedAt = now
	stored.UpdatedAt = now
	if stored.Status == "" {
		stored.Status = StatusPending
	}
	m.transactions[stored.ID] = &stored
	m.order = append(m.order, stored.ID)

	result := stored
	return &result, nil
}

func (m *MemoryRepository) GetTransactionByID(_ context.Context, id string) (*Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	txn, ok := m.transactions[id]
	if !ok {
		return nil, ErrNotFound
	}
	result := *txn
	return &result, nil
}

func (m *MemoryRepository) ListTransactionsByUserID(_ context.Context, userID string) ([]Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Transaction
	for _, txn := range m.transactions {
		if txn.UserID == userID {
			result = append(result, *txn)
		}
	}

	// Sort by created_at DESC (most recent first).
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result, nil
}

func (m *MemoryRepository) UpdateTransactionStatus(_ context.Context, id string, status TransactionStatus, reason *string) (*Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	txn, ok := m.transactions[id]
	if !ok {
		return nil, ErrNotFound
	}

	if err := ValidateTransition(txn.Status, status); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
	}

	txn.Status = status
	txn.StatusReason = reason
	txn.UpdatedAt = time.Now().UTC()

	result := *txn
	return &result, nil
}
