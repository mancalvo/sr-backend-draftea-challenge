package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- MemoryRepository tests validate domain invariants ---

func TestMemoryRepo_CreateTransaction_Success(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeDeposit,
		Status:   StatusPending,
		Amount:   10000,
		Currency: "ARS",
	}

	created, err := repo.CreateTransaction(ctx, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ID != "txn-1" {
		t.Errorf("id = %v, want txn-1", created.ID)
	}
	if created.Status != StatusPending {
		t.Errorf("status = %v, want pending", created.Status)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}
}

func TestMemoryRepo_CreateTransaction_DefaultPendingStatus(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypePurchase,
		Amount:   5000,
		Currency: "ARS",
	}

	created, err := repo.CreateTransaction(ctx, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Status != StatusPending {
		t.Errorf("status = %v, want pending (default)", created.Status)
	}
}

func TestMemoryRepo_CreateTransaction_DuplicateID(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeDeposit,
		Status:   StatusPending,
		Amount:   10000,
		Currency: "ARS",
	}

	_, err := repo.CreateTransaction(ctx, txn)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = repo.CreateTransaction(ctx, txn)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("expected ErrDuplicateID, got %v", err)
	}
}

func TestMemoryRepo_GetTransactionByID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.GetTransactionByID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepo_GetTransactionByID_Found(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID:       "txn-1",
		UserID:   "user-1",
		Type:     TransactionTypeDeposit,
		Status:   StatusPending,
		Amount:   10000,
		Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn)

	found, err := repo.GetTransactionByID(ctx, "txn-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID != "txn-1" {
		t.Errorf("id = %v, want txn-1", found.ID)
	}
	if found.Amount != 10000 {
		t.Errorf("amount = %v, want 10000", found.Amount)
	}
}

func TestMemoryRepo_ListTransactionsByUserID_Empty(t *testing.T) {
	repo := NewMemoryRepository()
	txns, err := repo.ListTransactionsByUserID(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 0 {
		t.Errorf("expected 0 transactions, got %d", len(txns))
	}
}

func TestMemoryRepo_ListTransactionsByUserID_OrderedByCreatedAtDesc(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Create transactions with slight time gaps to ensure ordering.
	txn1 := &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn1)

	// Ensure time progresses.
	time.Sleep(2 * time.Millisecond)

	txn2 := &Transaction{
		ID: "txn-2", UserID: "user-1", Type: TransactionTypePurchase,
		Status: StatusPending, Amount: 2000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn2)

	time.Sleep(2 * time.Millisecond)

	txn3 := &Transaction{
		ID: "txn-3", UserID: "user-1", Type: TransactionTypeRefund,
		Status: StatusPending, Amount: 500, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn3)

	// Different user should not appear.
	txnOther := &Transaction{
		ID: "txn-other", UserID: "user-2", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 9999, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txnOther)

	txns, err := repo.ListTransactionsByUserID(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(txns))
	}

	// Most recent first.
	if txns[0].ID != "txn-3" {
		t.Errorf("first = %v, want txn-3 (most recent)", txns[0].ID)
	}
	if txns[1].ID != "txn-2" {
		t.Errorf("second = %v, want txn-2", txns[1].ID)
	}
	if txns[2].ID != "txn-1" {
		t.Errorf("third = %v, want txn-1 (oldest)", txns[2].ID)
	}
}

// --- State transition tests ---

func TestMemoryRepo_UpdateStatus_LegalTransitions(t *testing.T) {
	tests := []struct {
		name string
		from TransactionStatus
		to   TransactionStatus
	}{
		{"pending to completed", StatusPending, StatusCompleted},
		{"pending to failed", StatusPending, StatusFailed},
		{"pending to timed_out", StatusPending, StatusTimedOut},
		{"pending to compensated", StatusPending, StatusCompensated},
		{"pending to reconciliation_required", StatusPending, StatusReconciliationRequired},
		{"timed_out to completed", StatusTimedOut, StatusCompleted},
		{"timed_out to failed", StatusTimedOut, StatusFailed},
		{"timed_out to reconciliation_required", StatusTimedOut, StatusReconciliationRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewMemoryRepository()
			ctx := context.Background()

			txn := &Transaction{
				ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
				Status: tt.from, Amount: 1000, Currency: "ARS",
			}
			_, _ = repo.CreateTransaction(ctx, txn)

			updated, err := repo.UpdateTransactionStatus(ctx, "txn-1", tt.to, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if updated.Status != tt.to {
				t.Errorf("status = %v, want %v", updated.Status, tt.to)
			}
		})
	}
}

func TestMemoryRepo_UpdateStatus_IllegalTransitions(t *testing.T) {
	tests := []struct {
		name string
		from TransactionStatus
		to   TransactionStatus
	}{
		{"completed to pending", StatusCompleted, StatusPending},
		{"completed to failed", StatusCompleted, StatusFailed},
		{"failed to pending", StatusFailed, StatusPending},
		{"failed to completed", StatusFailed, StatusCompleted},
		{"compensated to pending", StatusCompensated, StatusPending},
		{"compensated to completed", StatusCompensated, StatusCompleted},
		{"reconciliation_required to pending", StatusReconciliationRequired, StatusPending},
		{"timed_out to compensated", StatusTimedOut, StatusCompensated},
		{"timed_out to pending", StatusTimedOut, StatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewMemoryRepository()
			ctx := context.Background()

			txn := &Transaction{
				ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
				Status: tt.from, Amount: 1000, Currency: "ARS",
			}
			_, _ = repo.CreateTransaction(ctx, txn)

			_, err := repo.UpdateTransactionStatus(ctx, "txn-1", tt.to, nil)
			if !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("expected ErrIllegalTransition, got %v", err)
			}
		})
	}
}

func TestMemoryRepo_UpdateStatus_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.UpdateTransactionStatus(context.Background(), "nonexistent", StatusCompleted, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepo_UpdateStatus_WithReason(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn)

	reason := "provider declined the charge"
	updated, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusFailed, &reason)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.StatusReason == nil || *updated.StatusReason != reason {
		t.Errorf("status_reason = %v, want %q", updated.StatusReason, reason)
	}
}

func TestMemoryRepo_UpdateStatus_TimedOutThenCompleted(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn)

	// First transition to timed_out.
	_, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusTimedOut, nil)
	if err != nil {
		t.Fatalf("timeout transition: %v", err)
	}

	// Late event resolves to completed.
	_, err = repo.UpdateTransactionStatus(ctx, "txn-1", StatusCompleted, nil)
	if err != nil {
		t.Fatalf("completed after timeout: %v", err)
	}

	found, _ := repo.GetTransactionByID(ctx, "txn-1")
	if found.Status != StatusCompleted {
		t.Errorf("status = %v, want completed", found.Status)
	}
}
