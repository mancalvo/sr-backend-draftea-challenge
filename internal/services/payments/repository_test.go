package payments

import (
	"context"
	"errors"
	"fmt"
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

func TestMemoryRepo_CreateTransaction_WithAuditFields(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	originalTransactionID := "txn-purchase-1"
	providerReference := "sim-txn-1"
	txn := &Transaction{
		ID:                    "txn-1",
		UserID:                "user-1",
		Type:                  TransactionTypeRefund,
		Amount:                5000,
		Currency:              "ARS",
		OriginalTransactionID: &originalTransactionID,
		ProviderReference:     &providerReference,
	}

	created, err := repo.CreateTransaction(ctx, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.OriginalTransactionID == nil || *created.OriginalTransactionID != originalTransactionID {
		t.Errorf("original_transaction_id = %v, want %q", created.OriginalTransactionID, originalTransactionID)
	}
	if created.ProviderReference == nil || *created.ProviderReference != providerReference {
		t.Errorf("provider_reference = %v, want %q", created.ProviderReference, providerReference)
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

			updated, err := repo.UpdateTransactionStatus(ctx, "txn-1", tt.to, nil, nil)
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

			_, err := repo.UpdateTransactionStatus(ctx, "txn-1", tt.to, nil, nil)
			if !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("expected ErrIllegalTransition, got %v", err)
			}
		})
	}
}

func TestMemoryRepo_UpdateStatus_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.UpdateTransactionStatus(context.Background(), "nonexistent", StatusCompleted, nil, nil)
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
	updated, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusFailed, &reason, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.StatusReason == nil || *updated.StatusReason != reason {
		t.Errorf("status_reason = %v, want %q", updated.StatusReason, reason)
	}
}

func TestMemoryRepo_UpdateStatus_SameTargetAndReason_IsIdempotent(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn)

	reason := "provider declined the charge"
	first, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusFailed, &reason, nil)
	if err != nil {
		t.Fatalf("first update: %v", err)
	}

	second, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusFailed, &reason, nil)
	if err != nil {
		t.Fatalf("second update: %v", err)
	}

	if second.Status != StatusFailed {
		t.Errorf("status = %v, want %v", second.Status, StatusFailed)
	}
	if second.StatusReason == nil || *second.StatusReason != reason {
		t.Errorf("status_reason = %v, want %q", second.StatusReason, reason)
	}
	if second.UpdatedAt.Before(first.UpdatedAt) {
		t.Errorf("updated_at moved backwards: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

func TestMemoryRepo_UpdateStatus_SameStatusCanRecordProviderReference(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn)

	providerReference := "sim-txn-1"
	updated, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusPending, nil, &providerReference)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.ProviderReference == nil || *updated.ProviderReference != providerReference {
		t.Errorf("provider_reference = %v, want %q", updated.ProviderReference, providerReference)
	}
}

func TestMemoryRepo_UpdateStatus_PreservesProviderReferenceOnLaterTransition(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	txn := &Transaction{
		ID: "txn-1", UserID: "user-1", Type: TransactionTypeDeposit,
		Status: StatusPending, Amount: 1000, Currency: "ARS",
	}
	_, _ = repo.CreateTransaction(ctx, txn)

	providerReference := "sim-txn-1"
	_, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusPending, nil, &providerReference)
	if err != nil {
		t.Fatalf("record provider reference: %v", err)
	}

	updated, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusCompleted, nil, nil)
	if err != nil {
		t.Fatalf("transition to completed: %v", err)
	}
	if updated.ProviderReference == nil || *updated.ProviderReference != providerReference {
		t.Errorf("provider_reference = %v, want %q", updated.ProviderReference, providerReference)
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
	_, err := repo.UpdateTransactionStatus(ctx, "txn-1", StatusTimedOut, nil, nil)
	if err != nil {
		t.Fatalf("timeout transition: %v", err)
	}

	// Late event resolves to completed.
	_, err = repo.UpdateTransactionStatus(ctx, "txn-1", StatusCompleted, nil, nil)
	if err != nil {
		t.Fatalf("completed after timeout: %v", err)
	}

	found, _ := repo.GetTransactionByID(ctx, "txn-1")
	if found.Status != StatusCompleted {
		t.Errorf("status = %v, want completed", found.Status)
	}
}

// --- Paginated listing tests ---

func TestMemoryRepo_ListPaginated_DefaultLimit(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Create 3 transactions — fewer than DefaultPageLimit.
	for i := 0; i < 3; i++ {
		time.Sleep(time.Millisecond)
		repo.CreateTransaction(ctx, &Transaction{
			ID: "txn-" + string(rune('a'+i)), UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
		})
	}

	result, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Transactions) != 3 {
		t.Errorf("expected 3 transactions, got %d", len(result.Transactions))
	}
	if result.HasMore {
		t.Error("expected has_more=false")
	}
	if result.NextCursor != nil {
		t.Error("expected next_cursor=nil")
	}
}

func TestMemoryRepo_ListPaginated_ExplicitLimit(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond)
		repo.CreateTransaction(ctx, &Transaction{
			ID: fmt.Sprintf("txn-%d", i), UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
		})
	}

	result, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Transactions) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(result.Transactions))
	}
	if !result.HasMore {
		t.Error("expected has_more=true")
	}
	if result.NextCursor == nil {
		t.Fatal("expected next_cursor to be set")
	}
}

func TestMemoryRepo_ListPaginated_CursorTraversal(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Create 5 transactions with distinct timestamps.
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Millisecond)
		repo.CreateTransaction(ctx, &Transaction{
			ID: fmt.Sprintf("txn-%d", i), UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: int64(1000 * (i + 1)), Currency: "ARS",
		})
	}

	// Page 1: limit 2.
	page1, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Transactions) != 2 {
		t.Fatalf("page 1: expected 2, got %d", len(page1.Transactions))
	}
	if !page1.HasMore {
		t.Error("page 1: expected has_more=true")
	}
	// Most recent first (txn-4, txn-3).
	if page1.Transactions[0].ID != "txn-4" {
		t.Errorf("page 1[0] = %s, want txn-4", page1.Transactions[0].ID)
	}
	if page1.Transactions[1].ID != "txn-3" {
		t.Errorf("page 1[1] = %s, want txn-3", page1.Transactions[1].ID)
	}

	// Page 2: use cursor from page 1.
	cursor1, err := DecodeCursor(*page1.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	page2, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  2,
		Cursor: cursor1,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Transactions) != 2 {
		t.Fatalf("page 2: expected 2, got %d", len(page2.Transactions))
	}
	if !page2.HasMore {
		t.Error("page 2: expected has_more=true")
	}
	if page2.Transactions[0].ID != "txn-2" {
		t.Errorf("page 2[0] = %s, want txn-2", page2.Transactions[0].ID)
	}
	if page2.Transactions[1].ID != "txn-1" {
		t.Errorf("page 2[1] = %s, want txn-1", page2.Transactions[1].ID)
	}

	// Page 3: last page.
	cursor2, _ := DecodeCursor(*page2.NextCursor)
	page3, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  2,
		Cursor: cursor2,
	})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3.Transactions) != 1 {
		t.Fatalf("page 3: expected 1, got %d", len(page3.Transactions))
	}
	if page3.HasMore {
		t.Error("page 3: expected has_more=false")
	}
	if page3.NextCursor != nil {
		t.Error("page 3: expected next_cursor=nil")
	}
	if page3.Transactions[0].ID != "txn-0" {
		t.Errorf("page 3[0] = %s, want txn-0", page3.Transactions[0].ID)
	}

	// Verify no duplicates across all pages.
	seen := make(map[string]bool)
	for _, page := range []*ListTransactionsResult{page1, page2, page3} {
		for _, txn := range page.Transactions {
			if seen[txn.ID] {
				t.Errorf("duplicate transaction: %s", txn.ID)
			}
			seen[txn.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("expected 5 unique transactions, got %d", len(seen))
	}
}

func TestMemoryRepo_ListPaginated_StableOrderSameCreatedAt(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Force same created_at by inserting directly into the map.
	now := time.Now().UTC()
	for _, id := range []string{"txn-c", "txn-a", "txn-b"} {
		txn := &Transaction{
			ID: id, UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
			CreatedAt: now, UpdatedAt: now,
		}
		repo.mu.Lock()
		repo.transactions[id] = txn
		repo.order = append(repo.order, id)
		repo.mu.Unlock()
	}

	result, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(result.Transactions))
	}

	// When created_at is the same, order by ID DESC: c, b, a.
	expected := []string{"txn-c", "txn-b", "txn-a"}
	for i, want := range expected {
		if result.Transactions[i].ID != want {
			t.Errorf("position %d: got %s, want %s", i, result.Transactions[i].ID, want)
		}
	}
}

func TestMemoryRepo_ListPaginated_CursorWithTiedTimestamps(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// All share the same timestamp — cursor must use ID as tie-breaker.
	now := time.Now().UTC()
	for _, id := range []string{"txn-d", "txn-c", "txn-b", "txn-a"} {
		txn := &Transaction{
			ID: id, UserID: "user-1",
			Type: TransactionTypeDeposit, Status: StatusPending,
			Amount: 1000, Currency: "ARS",
			CreatedAt: now, UpdatedAt: now,
		}
		repo.mu.Lock()
		repo.transactions[id] = txn
		repo.order = append(repo.order, id)
		repo.mu.Unlock()
	}

	// Page 1: limit 2 => txn-d, txn-c (DESC by ID).
	page1, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Transactions) != 2 {
		t.Fatalf("page 1: expected 2, got %d", len(page1.Transactions))
	}
	if page1.Transactions[0].ID != "txn-d" || page1.Transactions[1].ID != "txn-c" {
		t.Errorf("page 1: got [%s, %s], want [txn-d, txn-c]",
			page1.Transactions[0].ID, page1.Transactions[1].ID)
	}

	// Page 2: use cursor from page 1 => txn-b, txn-a.
	cursor, _ := DecodeCursor(*page1.NextCursor)
	page2, err := repo.ListTransactionsByUserIDPaginated(ctx, ListTransactionsQuery{
		UserID: "user-1",
		Limit:  2,
		Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Transactions) != 2 {
		t.Fatalf("page 2: expected 2, got %d", len(page2.Transactions))
	}
	if page2.Transactions[0].ID != "txn-b" || page2.Transactions[1].ID != "txn-a" {
		t.Errorf("page 2: got [%s, %s], want [txn-b, txn-a]",
			page2.Transactions[0].ID, page2.Transactions[1].ID)
	}
	if page2.HasMore {
		t.Error("page 2: expected has_more=false")
	}
}
