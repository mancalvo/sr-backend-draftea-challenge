package catalogaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors returned by repository operations.
var (
	ErrNotFound        = errors.New("not found")
	ErrDuplicateAccess = errors.New("active access already exists for user and offering")
	ErrNoActiveAccess  = errors.New("no active access found")
)

// Repository defines the persistence operations for the catalog-access domain.
type Repository interface {
	// GetUserByID returns a user by their ID.
	GetUserByID(ctx context.Context, userID string) (*User, error)

	// GetOfferingByID returns an offering by its ID.
	GetOfferingByID(ctx context.Context, offeringID string) (*Offering, error)

	// GetActiveAccess returns the active access record for a user+offering pair, or ErrNotFound.
	GetActiveAccess(ctx context.Context, userID, offeringID string) (*AccessRecord, error)

	// GetActiveAccessByTransaction returns the active access record linked to a transaction, or ErrNotFound.
	GetActiveAccessByTransaction(ctx context.Context, transactionID string) (*AccessRecord, error)

	// GetAccessByTransaction returns any access record linked to a transaction, regardless of status.
	GetAccessByTransaction(ctx context.Context, transactionID string) (*AccessRecord, error)

	// ListEntitlements returns all active entitlements (with offering name) for a user.
	ListEntitlements(ctx context.Context, userID string) ([]Entitlement, error)

	// GrantAccess inserts a new active access record. Returns ErrDuplicateAccess if
	// a unique-constraint violation occurs on (user_id, offering_id) WHERE status='active'.
	GrantAccess(ctx context.Context, userID, offeringID, transactionID string) (*AccessRecord, error)

	// RevokeAccess marks the active access record for the given transaction as revoked.
	// Returns ErrNoActiveAccess if no matching active record exists.
	RevokeAccess(ctx context.Context, transactionID string) (*AccessRecord, error)
}

// PostgresRepository implements Repository against the catalog_access PostgreSQL schema.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) GetUserByID(ctx context.Context, userID string) (*User, error) {
	const q = `SELECT id, email, name, created_at, updated_at
		FROM catalog_access.users WHERE id = $1`
	var u User
	err := r.db.QueryRowContext(ctx, q, userID).Scan(
		&u.ID, &u.Email, &u.Name, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", userID, err)
	}
	return &u, nil
}

func (r *PostgresRepository) GetOfferingByID(ctx context.Context, offeringID string) (*Offering, error) {
	const q = `SELECT id, name, description, price, currency, active, created_at, updated_at
		FROM catalog_access.offerings WHERE id = $1`
	var o Offering
	var desc sql.NullString
	err := r.db.QueryRowContext(ctx, q, offeringID).Scan(
		&o.ID, &o.Name, &desc, &o.Price, &o.Currency, &o.Active, &o.CreatedAt, &o.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get offering %s: %w", offeringID, err)
	}
	if desc.Valid {
		o.Description = desc.String
	}
	return &o, nil
}

func (r *PostgresRepository) GetActiveAccess(ctx context.Context, userID, offeringID string) (*AccessRecord, error) {
	const q = `SELECT id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at
		FROM catalog_access.access_records
		WHERE user_id = $1 AND offering_id = $2 AND status = 'active'`
	return r.scanAccessRecord(r.db.QueryRowContext(ctx, q, userID, offeringID))
}

func (r *PostgresRepository) GetActiveAccessByTransaction(ctx context.Context, transactionID string) (*AccessRecord, error) {
	const q = `SELECT id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at
		FROM catalog_access.access_records
		WHERE transaction_id = $1 AND status = 'active'`
	return r.scanAccessRecord(r.db.QueryRowContext(ctx, q, transactionID))
}

func (r *PostgresRepository) GetAccessByTransaction(ctx context.Context, transactionID string) (*AccessRecord, error) {
	const q = `SELECT id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at
		FROM catalog_access.access_records
		WHERE transaction_id = $1
		ORDER BY created_at DESC
		LIMIT 1`
	return r.scanAccessRecord(r.db.QueryRowContext(ctx, q, transactionID))
}

func (r *PostgresRepository) ListEntitlements(ctx context.Context, userID string) ([]Entitlement, error) {
	const q = `SELECT ar.offering_id, o.name, ar.transaction_id, ar.granted_at
		FROM catalog_access.access_records ar
		JOIN catalog_access.offerings o ON o.id = ar.offering_id
		WHERE ar.user_id = $1 AND ar.status = 'active'
		ORDER BY ar.granted_at DESC`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list entitlements for user %s: %w", userID, err)
	}
	defer rows.Close()

	var result []Entitlement
	for rows.Next() {
		var e Entitlement
		if err := rows.Scan(&e.OfferingID, &e.OfferingName, &e.TransactionID, &e.GrantedAt); err != nil {
			return nil, fmt.Errorf("scan entitlement: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlement rows: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) GrantAccess(ctx context.Context, userID, offeringID, transactionID string) (*AccessRecord, error) {
	const q = `INSERT INTO catalog_access.access_records (user_id, offering_id, transaction_id, status, granted_at)
		VALUES ($1, $2, $3, 'active', now())
		RETURNING id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at`
	row := r.db.QueryRowContext(ctx, q, userID, offeringID, transactionID)
	ar, err := r.scanAccessRecord(row)
	if err != nil {
		// Check for unique constraint violation on the partial unique index.
		if isUniqueViolation(err) {
			return nil, ErrDuplicateAccess
		}
		return nil, fmt.Errorf("grant access: %w", err)
	}
	return ar, nil
}

func (r *PostgresRepository) RevokeAccess(ctx context.Context, transactionID string) (*AccessRecord, error) {
	const q = `UPDATE catalog_access.access_records
		SET status = 'revoked', revoked_at = now(), updated_at = now()
		WHERE transaction_id = $1 AND status = 'active'
		RETURNING id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at`
	ar, err := r.scanAccessRecord(r.db.QueryRowContext(ctx, q, transactionID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNoActiveAccess
	}
	if err != nil {
		return nil, fmt.Errorf("revoke access: %w", err)
	}
	return ar, nil
}

// scanAccessRecord scans a single row into an AccessRecord.
func (r *PostgresRepository) scanAccessRecord(row *sql.Row) (*AccessRecord, error) {
	var a AccessRecord
	var revokedAt sql.NullTime
	err := row.Scan(
		&a.ID, &a.UserID, &a.OfferingID, &a.TransactionID,
		&a.Status, &a.GrantedAt, &revokedAt,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		a.RevokedAt = &t
	}
	return &a, nil
}

// isUniqueViolation checks if the error is a PostgreSQL unique_violation (23505).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq exposes the SQLSTATE code; check the error string as a fallback.
	return contains(err.Error(), "23505") || contains(err.Error(), "unique_violation") ||
		contains(err.Error(), "duplicate key")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
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
	Users         map[string]*User
	Offerings     map[string]*Offering
	AccessRecords []*AccessRecord
	idCounter     int
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		Users:     make(map[string]*User),
		Offerings: make(map[string]*Offering),
	}
}

func (m *MemoryRepository) GetUserByID(_ context.Context, userID string) (*User, error) {
	u, ok := m.Users[userID]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (m *MemoryRepository) GetOfferingByID(_ context.Context, offeringID string) (*Offering, error) {
	o, ok := m.Offerings[offeringID]
	if !ok {
		return nil, ErrNotFound
	}
	return o, nil
}

func (m *MemoryRepository) GetActiveAccess(_ context.Context, userID, offeringID string) (*AccessRecord, error) {
	for _, ar := range m.AccessRecords {
		if ar.UserID == userID && ar.OfferingID == offeringID && ar.Status == AccessStatusActive {
			return ar, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) GetActiveAccessByTransaction(_ context.Context, transactionID string) (*AccessRecord, error) {
	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID && ar.Status == AccessStatusActive {
			return ar, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) GetAccessByTransaction(_ context.Context, transactionID string) (*AccessRecord, error) {
	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID {
			return ar, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) ListEntitlements(_ context.Context, userID string) ([]Entitlement, error) {
	var result []Entitlement
	for _, ar := range m.AccessRecords {
		if ar.UserID == userID && ar.Status == AccessStatusActive {
			name := ""
			if o, ok := m.Offerings[ar.OfferingID]; ok {
				name = o.Name
			}
			result = append(result, Entitlement{
				OfferingID:    ar.OfferingID,
				OfferingName:  name,
				TransactionID: ar.TransactionID,
				GrantedAt:     ar.GrantedAt,
			})
		}
	}
	return result, nil
}

func (m *MemoryRepository) GrantAccess(_ context.Context, userID, offeringID, transactionID string) (*AccessRecord, error) {
	// Enforce unique active access per user+offering.
	for _, ar := range m.AccessRecords {
		if ar.UserID == userID && ar.OfferingID == offeringID && ar.Status == AccessStatusActive {
			return nil, ErrDuplicateAccess
		}
	}
	m.idCounter++
	now := time.Now().UTC()
	ar := &AccessRecord{
		ID:            fmt.Sprintf("ar-%d", m.idCounter),
		UserID:        userID,
		OfferingID:    offeringID,
		TransactionID: transactionID,
		Status:        AccessStatusActive,
		GrantedAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.AccessRecords = append(m.AccessRecords, ar)
	return ar, nil
}

func (m *MemoryRepository) RevokeAccess(_ context.Context, transactionID string) (*AccessRecord, error) {
	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID && ar.Status == AccessStatusActive {
			now := time.Now().UTC()
			ar.Status = AccessStatusRevoked
			ar.RevokedAt = &now
			ar.UpdatedAt = now
			return ar, nil
		}
	}
	return nil, ErrNoActiveAccess
}
