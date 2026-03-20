package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	platformdatabase "github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
)

// Sentinel errors returned by repository operations.
var (
	ErrNotFound        = errors.New("not found")
	ErrDuplicateAccess = errors.New("active access already exists for user and offering")
	ErrDuplicateGrant  = errors.New("access grant already processed for transaction")
	ErrNoActiveAccess  = errors.New("no active access found")
)

// Repository defines the persistence operations for the catalog-access domain.
type Repository interface {
	// GetUserByID returns a user by their ID.
	GetUserByID(ctx context.Context, userID string) (*domain.User, error)

	// GetOfferingByID returns an offering by its ID.
	GetOfferingByID(ctx context.Context, offeringID string) (*domain.Offering, error)

	// GetActiveAccess returns the active access record for a user+offering pair, or ErrNotFound.
	GetActiveAccess(ctx context.Context, userID, offeringID string) (*domain.AccessRecord, error)

	// GetActiveAccessByTransaction returns the active access record linked to a transaction, or ErrNotFound.
	GetActiveAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error)

	// GetAccessByTransaction returns any access record linked to a transaction, regardless of status.
	GetAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error)

	// ListEntitlements returns all active entitlements (with offering name) for a user.
	ListEntitlements(ctx context.Context, userID string) ([]domain.Entitlement, error)

	// GrantAccess inserts a new active access record. It returns ErrDuplicateGrant
	// when the transaction has already been processed, or ErrDuplicateAccess when
	// another active entitlement already exists for the same user and offering.
	GrantAccess(ctx context.Context, userID, offeringID, transactionID string) (*domain.AccessRecord, error)

	// RevokeAccess marks the active access record for the given transaction as revoked.
	// Returns ErrNoActiveAccess if no matching active record exists.
	RevokeAccess(ctx context.Context, transactionID string) (*domain.AccessRecord, error)
}

// PostgresRepository implements Repository against the catalog_access PostgreSQL schema.
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

func (r *PostgresRepository) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	const q = `SELECT id, email, name, created_at, updated_at
		FROM catalog_access.users WHERE id = $1`
	var u domain.User
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

func (r *PostgresRepository) GetOfferingByID(ctx context.Context, offeringID string) (*domain.Offering, error) {
	const q = `SELECT id, name, description, price, currency, active, created_at, updated_at
		FROM catalog_access.offerings WHERE id = $1`
	var o domain.Offering
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

func (r *PostgresRepository) GetActiveAccess(ctx context.Context, userID, offeringID string) (*domain.AccessRecord, error) {
	const q = `SELECT id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at
		FROM catalog_access.access_records
		WHERE user_id = $1 AND offering_id = $2 AND status = 'active'`
	return scanAccessRecord(r.db.QueryRowContext(ctx, q, userID, offeringID))
}

func (r *PostgresRepository) GetActiveAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error) {
	const q = `SELECT id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at
		FROM catalog_access.access_records
		WHERE transaction_id = $1 AND status = 'active'`
	return scanAccessRecord(r.db.QueryRowContext(ctx, q, transactionID))
}

func (r *PostgresRepository) GetAccessByTransaction(ctx context.Context, transactionID string) (*domain.AccessRecord, error) {
	const q = `SELECT id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at
		FROM catalog_access.access_records
		WHERE transaction_id = $1
		ORDER BY created_at DESC
		LIMIT 1`
	return scanAccessRecord(r.db.QueryRowContext(ctx, q, transactionID))
}

func (r *PostgresRepository) ListEntitlements(ctx context.Context, userID string) ([]domain.Entitlement, error) {
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

	var result []domain.Entitlement
	for rows.Next() {
		var e domain.Entitlement
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

func (r *PostgresRepository) GrantAccess(ctx context.Context, userID, offeringID, transactionID string) (*domain.AccessRecord, error) {
	const q = `INSERT INTO catalog_access.access_records (user_id, offering_id, transaction_id, status, granted_at)
		VALUES ($1, $2, $3, 'active', now())
		RETURNING id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at`
	row := r.db.QueryRowContext(ctx, q, userID, offeringID, transactionID)
	ar, err := scanAccessRecord(row)
	if err != nil {
		if platformdatabase.IsUniqueViolation(err) {
			existing, lookupErr := r.GetAccessByTransaction(ctx, transactionID)
			if lookupErr == nil {
				return existing, ErrDuplicateGrant
			}
			if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
				return nil, fmt.Errorf("lookup access by transaction after duplicate grant: %w", lookupErr)
			}
			return nil, ErrDuplicateAccess
		}
		return nil, fmt.Errorf("grant access: %w", err)
	}
	return ar, nil
}

func (r *PostgresRepository) RevokeAccess(ctx context.Context, transactionID string) (*domain.AccessRecord, error) {
	const q = `UPDATE catalog_access.access_records
		SET status = 'revoked', revoked_at = now(), updated_at = now()
		WHERE transaction_id = $1 AND status = 'active'
		RETURNING id, user_id, offering_id, transaction_id, status, granted_at, revoked_at, created_at, updated_at`
	ar, err := scanAccessRecord(r.db.QueryRowContext(ctx, q, transactionID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNoActiveAccess
	}
	if err != nil {
		return nil, fmt.Errorf("revoke access: %w", err)
	}
	return ar, nil
}

// scanAccessRecord scans a single row into an AccessRecord.
func scanAccessRecord(row scanner) (*domain.AccessRecord, error) {
	var a domain.AccessRecord
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

// ---- In-memory repository for testing ----

// MemoryRepository is an in-memory implementation of Repository for unit tests.
type MemoryRepository struct {
	mu            sync.RWMutex
	Users         map[string]*domain.User
	Offerings     map[string]*domain.Offering
	AccessRecords []*domain.AccessRecord
	idCounter     int
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		Users:     make(map[string]*domain.User),
		Offerings: make(map[string]*domain.Offering),
	}
}

func (m *MemoryRepository) GetUserByID(_ context.Context, userID string) (*domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.Users[userID]
	if !ok {
		return nil, ErrNotFound
	}
	return copyUser(u), nil
}

func (m *MemoryRepository) GetOfferingByID(_ context.Context, offeringID string) (*domain.Offering, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	o, ok := m.Offerings[offeringID]
	if !ok {
		return nil, ErrNotFound
	}
	return copyOffering(o), nil
}

func (m *MemoryRepository) GetActiveAccess(_ context.Context, userID, offeringID string) (*domain.AccessRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ar := range m.AccessRecords {
		if ar.UserID == userID && ar.OfferingID == offeringID && ar.Status == domain.AccessStatusActive {
			return copyAccessRecord(ar), nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) GetActiveAccessByTransaction(_ context.Context, transactionID string) (*domain.AccessRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID && ar.Status == domain.AccessStatusActive {
			return copyAccessRecord(ar), nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) GetAccessByTransaction(_ context.Context, transactionID string) (*domain.AccessRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID {
			return copyAccessRecord(ar), nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) ListEntitlements(_ context.Context, userID string) ([]domain.Entitlement, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []domain.Entitlement
	for _, ar := range m.AccessRecords {
		if ar.UserID == userID && ar.Status == domain.AccessStatusActive {
			name := ""
			if o, ok := m.Offerings[ar.OfferingID]; ok {
				name = o.Name
			}
			result = append(result, domain.Entitlement{
				OfferingID:    ar.OfferingID,
				OfferingName:  name,
				TransactionID: ar.TransactionID,
				GrantedAt:     ar.GrantedAt,
			})
		}
	}
	return result, nil
}

func (m *MemoryRepository) GrantAccess(_ context.Context, userID, offeringID, transactionID string) (*domain.AccessRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID {
			return copyAccessRecord(ar), ErrDuplicateGrant
		}
	}

	// Enforce unique active access per user+offering.
	for _, ar := range m.AccessRecords {
		if ar.UserID == userID && ar.OfferingID == offeringID && ar.Status == domain.AccessStatusActive {
			return nil, ErrDuplicateAccess
		}
	}
	m.idCounter++
	now := time.Now().UTC()
	ar := &domain.AccessRecord{
		ID:            fmt.Sprintf("ar-%d", m.idCounter),
		UserID:        userID,
		OfferingID:    offeringID,
		TransactionID: transactionID,
		Status:        domain.AccessStatusActive,
		GrantedAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.AccessRecords = append(m.AccessRecords, ar)
	return copyAccessRecord(ar), nil
}

func (m *MemoryRepository) RevokeAccess(_ context.Context, transactionID string) (*domain.AccessRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ar := range m.AccessRecords {
		if ar.TransactionID == transactionID && ar.Status == domain.AccessStatusActive {
			now := time.Now().UTC()
			ar.Status = domain.AccessStatusRevoked
			ar.RevokedAt = &now
			ar.UpdatedAt = now
			return copyAccessRecord(ar), nil
		}
	}
	return nil, ErrNoActiveAccess
}

func copyUser(user *domain.User) *domain.User {
	if user == nil {
		return nil
	}
	cloned := *user
	return &cloned
}

func copyOffering(offering *domain.Offering) *domain.Offering {
	if offering == nil {
		return nil
	}
	cloned := *offering
	return &cloned
}

func copyAccessRecord(access *domain.AccessRecord) *domain.AccessRecord {
	if access == nil {
		return nil
	}
	cloned := *access
	if access.RevokedAt != nil {
		revokedAt := *access.RevokedAt
		cloned.RevokedAt = &revokedAt
	}
	return &cloned
}
