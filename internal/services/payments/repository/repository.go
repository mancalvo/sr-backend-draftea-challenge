package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"
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
	CreateTransaction(ctx context.Context, txn *domain.Transaction) (*domain.Transaction, error)

	// GetTransactionByID returns a single transaction by its ID, or ErrNotFound.
	GetTransactionByID(ctx context.Context, id string) (*domain.Transaction, error)

	// ListTransactionsByUserID returns transactions for a user, ordered by created_at DESC.
	ListTransactionsByUserID(ctx context.Context, userID string) ([]domain.Transaction, error)

	// ListTransactionsByUserIDPaginated returns a cursor-paginated slice of transactions
	// for a user, ordered by (created_at DESC, id DESC).
	ListTransactionsByUserIDPaginated(ctx context.Context, query domain.ListTransactionsQuery) (*domain.ListTransactionsResult, error)

	// UpdateTransactionStatus transitions a transaction to a new status.
	// It validates the transition legality before applying the change.
	// Returns ErrNotFound if the transaction does not exist, or ErrIllegalTransition
	// if the transition is not allowed.
	UpdateTransactionStatus(ctx context.Context, id string, status domain.TransactionStatus, reason *string, providerReference *string) (*domain.Transaction, error)
}

// PostgresRepository implements Repository against the payments PostgreSQL schema.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateTransaction(ctx context.Context, txn *domain.Transaction) (*domain.Transaction, error) {
	const q = `INSERT INTO payments.transactions (id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at`

	row := r.db.QueryRowContext(ctx, q,
		txn.ID, txn.UserID, txn.Type, txn.Status,
		txn.Amount, txn.Currency, txn.OfferingID, txn.OriginalTransactionID, txn.ProviderReference, txn.StatusReason,
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

func (r *PostgresRepository) GetTransactionByID(ctx context.Context, id string) (*domain.Transaction, error) {
	const q = `SELECT id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at
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

func (r *PostgresRepository) ListTransactionsByUserID(ctx context.Context, userID string) ([]domain.Transaction, error) {
	const q = `SELECT id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at
		FROM payments.transactions
		WHERE user_id = $1
		ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list transactions for user %s: %w", userID, err)
	}
	defer rows.Close()

	var result []domain.Transaction
	for rows.Next() {
		var t domain.Transaction
		var offeringID sql.NullString
		var originalTransactionID sql.NullString
		var providerReference sql.NullString
		var statusReason sql.NullString
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.Type, &t.Status,
			&t.Amount, &t.Currency, &offeringID, &originalTransactionID, &providerReference, &statusReason,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		if offeringID.Valid {
			t.OfferingID = &offeringID.String
		}
		if originalTransactionID.Valid {
			t.OriginalTransactionID = &originalTransactionID.String
		}
		if providerReference.Valid {
			t.ProviderReference = &providerReference.String
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

func (r *PostgresRepository) ListTransactionsByUserIDPaginated(ctx context.Context, query domain.ListTransactionsQuery) (*domain.ListTransactionsResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = domain.DefaultPageLimit
	}
	if limit > domain.MaxPageLimit {
		limit = domain.MaxPageLimit
	}

	// Fetch limit+1 rows so we can detect whether more pages exist.
	fetchLimit := limit + 1
	var rows *sql.Rows
	var err error

	if query.Cursor != nil {
		const q = `SELECT id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at
			FROM payments.transactions
			WHERE user_id = $1 AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC
			LIMIT $4`
		rows, err = r.db.QueryContext(ctx, q, query.UserID, query.Cursor.CreatedAt, query.Cursor.ID, fetchLimit)
	} else {
		const q = `SELECT id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at
			FROM payments.transactions
			WHERE user_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
		rows, err = r.db.QueryContext(ctx, q, query.UserID, fetchLimit)
	}
	if err != nil {
		return nil, fmt.Errorf("list paginated transactions for user %s: %w", query.UserID, err)
	}
	defer rows.Close()

	var result []domain.Transaction
	for rows.Next() {
		var t domain.Transaction
		var offeringID sql.NullString
		var originalTransactionID sql.NullString
		var providerReference sql.NullString
		var statusReason sql.NullString
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.Type, &t.Status,
			&t.Amount, &t.Currency, &offeringID, &originalTransactionID, &providerReference, &statusReason,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		if offeringID.Valid {
			t.OfferingID = &offeringID.String
		}
		if originalTransactionID.Valid {
			t.OriginalTransactionID = &originalTransactionID.String
		}
		if providerReference.Valid {
			t.ProviderReference = &providerReference.String
		}
		if statusReason.Valid {
			t.StatusReason = &statusReason.String
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transaction rows: %w", err)
	}

	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}

	var nextCursor *string
	if hasMore && len(result) > 0 {
		last := result[len(result)-1]
		encoded := domain.EncodeCursor(domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		nextCursor = &encoded
	}

	return &domain.ListTransactionsResult{
		Transactions: result,
		NextCursor:   nextCursor,
		HasMore:      hasMore,
	}, nil
}

func (r *PostgresRepository) UpdateTransactionStatus(ctx context.Context, id string, status domain.TransactionStatus, reason *string, providerReference *string) (*domain.Transaction, error) {
	// Use a database transaction to ensure atomicity of read-check-update.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read current status with row lock.
	const selectQ = `SELECT id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at
		FROM payments.transactions WHERE id = $1 FOR UPDATE`
	var current domain.Transaction
	var offeringID sql.NullString
	var originalTransactionID sql.NullString
	var currentProviderReference sql.NullString
	var statusReason sql.NullString
	if err := tx.QueryRowContext(ctx, selectQ, id).Scan(
		&current.ID, &current.UserID, &current.Type, &current.Status,
		&current.Amount, &current.Currency, &offeringID, &originalTransactionID, &currentProviderReference, &statusReason,
		&current.CreatedAt, &current.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read current status: %w", err)
	}
	if offeringID.Valid {
		current.OfferingID = &offeringID.String
	}
	if originalTransactionID.Valid {
		current.OriginalTransactionID = &originalTransactionID.String
	}
	if currentProviderReference.Valid {
		current.ProviderReference = &currentProviderReference.String
	}
	if statusReason.Valid {
		current.StatusReason = &statusReason.String
	}

	effectiveProviderReference := current.ProviderReference
	if providerReference != nil {
		effectiveProviderReference = providerReference
	}

	if current.Status == status && sameOptionalString(current.StatusReason, reason) && sameOptionalString(current.ProviderReference, effectiveProviderReference) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit tx: %w", err)
		}
		return &current, nil
	}

	// Validate the transition.
	if current.Status != status {
		if err := domain.ValidateTransition(current.Status, status); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
		}
	}

	// Apply the update.
	const updateQ = `UPDATE payments.transactions
		SET status = $1, status_reason = $2, provider_reference = $3, updated_at = now()
		WHERE id = $4
		RETURNING id, user_id, type, status, amount, currency, offering_id, original_transaction_id, provider_reference, status_reason, created_at, updated_at`
	row := tx.QueryRowContext(ctx, updateQ, status, reason, effectiveProviderReference, id)

	var t domain.Transaction
	var updatedOfferingID sql.NullString
	var updatedOriginalTransactionID sql.NullString
	var updatedProviderReference sql.NullString
	var updatedStatusReason sql.NullString
	if err := row.Scan(
		&t.ID, &t.UserID, &t.Type, &t.Status,
		&t.Amount, &t.Currency, &updatedOfferingID, &updatedOriginalTransactionID, &updatedProviderReference, &updatedStatusReason,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan updated transaction: %w", err)
	}
	if updatedOfferingID.Valid {
		t.OfferingID = &updatedOfferingID.String
	}
	if updatedOriginalTransactionID.Valid {
		t.OriginalTransactionID = &updatedOriginalTransactionID.String
	}
	if updatedProviderReference.Valid {
		t.ProviderReference = &updatedProviderReference.String
	}
	if updatedStatusReason.Valid {
		t.StatusReason = &updatedStatusReason.String
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return &t, nil
}

// scanTransaction scans a single row into a Transaction.
func scanTransaction(row *sql.Row) (*domain.Transaction, error) {
	var t domain.Transaction
	var offeringID sql.NullString
	var originalTransactionID sql.NullString
	var providerReference sql.NullString
	var statusReason sql.NullString
	err := row.Scan(
		&t.ID, &t.UserID, &t.Type, &t.Status,
		&t.Amount, &t.Currency, &offeringID, &originalTransactionID, &providerReference, &statusReason,
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
	if originalTransactionID.Valid {
		t.OriginalTransactionID = &originalTransactionID.String
	}
	if providerReference.Valid {
		t.ProviderReference = &providerReference.String
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
	transactions map[string]*domain.Transaction
	order        []string // insertion order for listing
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		transactions: make(map[string]*domain.Transaction),
	}
}

// SeedTransaction stores a transaction as-is in the in-memory repository.
// It exists to support deterministic tests that need explicit timestamps.
func (m *MemoryRepository) SeedTransaction(txn *domain.Transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored := *txn
	if stored.Status == "" {
		stored.Status = domain.StatusPending
	}
	m.transactions[stored.ID] = &stored
	m.order = append(m.order, stored.ID)
}

func (m *MemoryRepository) CreateTransaction(_ context.Context, txn *domain.Transaction) (*domain.Transaction, error) {
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
		stored.Status = domain.StatusPending
	}
	m.transactions[stored.ID] = &stored
	m.order = append(m.order, stored.ID)

	result := stored
	return &result, nil
}

func (m *MemoryRepository) GetTransactionByID(_ context.Context, id string) (*domain.Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	txn, ok := m.transactions[id]
	if !ok {
		return nil, ErrNotFound
	}
	result := *txn
	return &result, nil
}

func (m *MemoryRepository) ListTransactionsByUserID(_ context.Context, userID string) ([]domain.Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []domain.Transaction
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

func (m *MemoryRepository) ListTransactionsByUserIDPaginated(_ context.Context, query domain.ListTransactionsQuery) (*domain.ListTransactionsResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limit := query.Limit
	if limit <= 0 {
		limit = domain.DefaultPageLimit
	}
	if limit > domain.MaxPageLimit {
		limit = domain.MaxPageLimit
	}

	// Collect all transactions for this user.
	var all []domain.Transaction
	for _, txn := range m.transactions {
		if txn.UserID == query.UserID {
			all = append(all, *txn)
		}
	}

	// Sort by (created_at DESC, id DESC).
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	// Apply cursor filter: skip items until we pass the cursor position.
	if query.Cursor != nil {
		idx := 0
		for idx < len(all) {
			t := all[idx]
			// In descending order, keep only items where (created_at, id) < (cursor.CreatedAt, cursor.ID).
			if t.CreatedAt.Before(query.Cursor.CreatedAt) ||
				(t.CreatedAt.Equal(query.Cursor.CreatedAt) && t.ID < query.Cursor.ID) {
				break
			}
			idx++
		}
		all = all[idx:]
	}

	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}

	var nextCursor *string
	if hasMore && len(all) > 0 {
		last := all[len(all)-1]
		encoded := domain.EncodeCursor(domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		nextCursor = &encoded
	}

	return &domain.ListTransactionsResult{
		Transactions: all,
		NextCursor:   nextCursor,
		HasMore:      hasMore,
	}, nil
}

func (m *MemoryRepository) UpdateTransactionStatus(_ context.Context, id string, status domain.TransactionStatus, reason *string, providerReference *string) (*domain.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	txn, ok := m.transactions[id]
	if !ok {
		return nil, ErrNotFound
	}

	effectiveProviderReference := txn.ProviderReference
	if providerReference != nil {
		effectiveProviderReference = providerReference
	}

	if txn.Status == status && sameOptionalString(txn.StatusReason, reason) && sameOptionalString(txn.ProviderReference, effectiveProviderReference) {
		result := *txn
		return &result, nil
	}

	if txn.Status != status {
		if err := domain.ValidateTransition(txn.Status, status); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrIllegalTransition, err)
		}
	}

	txn.Status = status
	txn.StatusReason = reason
	txn.ProviderReference = effectiveProviderReference
	txn.UpdatedAt = time.Now().UTC()

	result := *txn
	return &result, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
