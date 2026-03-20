package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
)

type (
	User         = domain.User
	Offering     = domain.Offering
	AccessRecord = domain.AccessRecord
)

const (
	AccessStatusActive  = domain.AccessStatusActive
	AccessStatusRevoked = domain.AccessStatusRevoked
)

func seedRepo(withAccess bool) *MemoryRepository {
	repo := NewMemoryRepository()
	repo.Users["user-1"] = &User{
		ID:    "user-1",
		Email: "test@example.com",
		Name:  "Test User",
	}
	repo.Offerings["offering-1"] = &Offering{
		ID:       "offering-1",
		Name:     "Premium Plan",
		Price:    10000,
		Currency: "ARS",
		Active:   true,
	}
	repo.Offerings["offering-inactive"] = &Offering{
		ID:       "offering-inactive",
		Name:     "Deprecated Plan",
		Price:    5000,
		Currency: "ARS",
		Active:   false,
	}
	if withAccess {
		now := time.Now().UTC()
		repo.AccessRecords = append(repo.AccessRecords, &AccessRecord{
			ID:            "ar-seed",
			UserID:        "user-1",
			OfferingID:    "offering-1",
			TransactionID: "txn-original",
			Status:        AccessStatusActive,
			GrantedAt:     now,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	return repo
}

// --- MemoryRepository tests validate the domain invariants ---

func TestMemoryRepo_GrantAccess_UniqueActiveAccess(t *testing.T) {
	repo := seedRepo(false)
	ctx := context.Background()

	// First grant should succeed.
	ar, err := repo.GrantAccess(ctx, "user-1", "offering-1", "txn-1")
	if err != nil {
		t.Fatalf("first grant: unexpected error: %v", err)
	}
	if ar.Status != AccessStatusActive {
		t.Errorf("status = %v, want active", ar.Status)
	}

	// Second grant to the same user+offering should fail with ErrDuplicateAccess.
	_, err = repo.GrantAccess(ctx, "user-1", "offering-1", "txn-2")
	if !errors.Is(err, ErrDuplicateAccess) {
		t.Fatalf("second grant: expected ErrDuplicateAccess, got %v", err)
	}

	// Different offering for same user should succeed.
	repo.Offerings["offering-2"] = &Offering{ID: "offering-2", Name: "Other Plan", Price: 1000, Currency: "ARS", Active: true}
	_, err = repo.GrantAccess(ctx, "user-1", "offering-2", "txn-3")
	if err != nil {
		t.Fatalf("different offering grant: unexpected error: %v", err)
	}
}

func TestMemoryRepo_GrantAccess_AfterRevoke(t *testing.T) {
	repo := seedRepo(false)
	ctx := context.Background()

	// Grant access.
	_, err := repo.GrantAccess(ctx, "user-1", "offering-1", "txn-1")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	// Revoke it.
	_, err = repo.RevokeAccess(ctx, "txn-1")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Should be able to grant again (no active access exists anymore).
	ar, err := repo.GrantAccess(ctx, "user-1", "offering-1", "txn-2")
	if err != nil {
		t.Fatalf("re-grant: unexpected error: %v", err)
	}
	if ar.TransactionID != "txn-2" {
		t.Errorf("transaction_id = %v, want txn-2", ar.TransactionID)
	}
}

func TestMemoryRepo_RevokeAccess_InactiveRejected(t *testing.T) {
	repo := seedRepo(false)
	ctx := context.Background()

	// No access records exist at all.
	_, err := repo.RevokeAccess(ctx, "txn-nonexistent")
	if !errors.Is(err, ErrNoActiveAccess) {
		t.Fatalf("expected ErrNoActiveAccess, got %v", err)
	}
}

func TestMemoryRepo_RevokeAccess_AlreadyRevokedRejected(t *testing.T) {
	repo := seedRepo(false)
	ctx := context.Background()

	// Grant and then revoke.
	_, err := repo.GrantAccess(ctx, "user-1", "offering-1", "txn-1")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	_, err = repo.RevokeAccess(ctx, "txn-1")
	if err != nil {
		t.Fatalf("first revoke: %v", err)
	}

	// Second revoke should fail.
	_, err = repo.RevokeAccess(ctx, "txn-1")
	if !errors.Is(err, ErrNoActiveAccess) {
		t.Fatalf("expected ErrNoActiveAccess on double revoke, got %v", err)
	}
}

func TestMemoryRepo_ListEntitlements(t *testing.T) {
	repo := seedRepo(false)
	ctx := context.Background()

	// No entitlements initially.
	ents, err := repo.ListEntitlements(ctx, "user-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ents) != 0 {
		t.Errorf("expected 0 entitlements, got %d", len(ents))
	}

	// Grant access, then list.
	_, _ = repo.GrantAccess(ctx, "user-1", "offering-1", "txn-1")
	ents, err = repo.ListEntitlements(ctx, "user-1")
	if err != nil {
		t.Fatalf("list after grant: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("expected 1 entitlement, got %d", len(ents))
	}
	if ents[0].OfferingName != "Premium Plan" {
		t.Errorf("offering_name = %v, want Premium Plan", ents[0].OfferingName)
	}

	// Revoke, then list should be empty again.
	_, _ = repo.RevokeAccess(ctx, "txn-1")
	ents, err = repo.ListEntitlements(ctx, "user-1")
	if err != nil {
		t.Fatalf("list after revoke: %v", err)
	}
	if len(ents) != 0 {
		t.Errorf("expected 0 entitlements after revoke, got %d", len(ents))
	}
}

func TestMemoryRepo_GetUserByID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.GetUserByID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepo_GetOfferingByID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.GetOfferingByID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepo_GetActiveAccess_NotFound(t *testing.T) {
	repo := seedRepo(false)
	_, err := repo.GetActiveAccess(context.Background(), "user-1", "offering-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepo_GetActiveAccessByTransaction_NotFound(t *testing.T) {
	repo := seedRepo(false)
	_, err := repo.GetActiveAccessByTransaction(context.Background(), "txn-nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
