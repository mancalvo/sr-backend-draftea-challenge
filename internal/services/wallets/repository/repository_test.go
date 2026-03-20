package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/domain"
)

type Wallet = domain.Wallet

const (
	MovementTypeCredit = domain.MovementTypeCredit
	MovementTypeDebit  = domain.MovementTypeDebit
)

// seedRepo creates a MemoryRepository with one wallet for user-1.
func seedRepo(balance int64) *MemoryRepository {
	repo := NewMemoryRepository()
	now := time.Now().UTC()
	repo.Wallets["user-1"] = &Wallet{
		ID:        "wallet-1",
		UserID:    "user-1",
		Balance:   balance,
		Currency:  "ARS",
		CreatedAt: now,
		UpdatedAt: now,
	}
	return repo
}

// --- Debit tests ---

func TestMemoryRepo_Debit_Success(t *testing.T) {
	repo := seedRepo(10000)
	ctx := context.Background()

	result, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 3000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Wallet.Balance != 7000 {
		t.Errorf("balance = %d, want 7000", result.Wallet.Balance)
	}
	if result.Movement.Type != MovementTypeDebit {
		t.Errorf("movement type = %v, want debit", result.Movement.Type)
	}
	if result.Movement.Amount != 3000 {
		t.Errorf("movement amount = %d, want 3000", result.Movement.Amount)
	}
	if result.Movement.BalanceBefore != 10000 {
		t.Errorf("balance_before = %d, want 10000", result.Movement.BalanceBefore)
	}
	if result.Movement.BalanceAfter != 7000 {
		t.Errorf("balance_after = %d, want 7000", result.Movement.BalanceAfter)
	}
	if result.Movement.TransactionID != "txn-1" {
		t.Errorf("transaction_id = %v, want txn-1", result.Movement.TransactionID)
	}
	if result.Movement.SourceStep != "purchase_debit" {
		t.Errorf("source_step = %v, want purchase_debit", result.Movement.SourceStep)
	}

	// Verify the wallet balance was actually updated in the repo.
	w, _ := repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 7000 {
		t.Errorf("repo wallet balance = %d, want 7000", w.Balance)
	}

	// Verify movement was recorded.
	if len(repo.Movements) != 1 {
		t.Fatalf("expected 1 movement, got %d", len(repo.Movements))
	}
}

func TestMemoryRepo_Debit_InsufficientFunds(t *testing.T) {
	repo := seedRepo(2000)
	ctx := context.Background()

	_, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 5000)
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}

	// Verify balance was not changed.
	w, _ := repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 2000 {
		t.Errorf("balance should remain 2000, got %d", w.Balance)
	}

	// Verify no movement was recorded.
	if len(repo.Movements) != 0 {
		t.Errorf("expected 0 movements, got %d", len(repo.Movements))
	}
}

func TestMemoryRepo_Debit_ExactBalance(t *testing.T) {
	repo := seedRepo(5000)
	ctx := context.Background()

	result, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Wallet.Balance != 0 {
		t.Errorf("balance = %d, want 0", result.Wallet.Balance)
	}
}

func TestMemoryRepo_Debit_WalletNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	_, err := repo.Debit(ctx, "nonexistent", "txn-1", "purchase_debit", 1000)
	if !errors.Is(err, ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestMemoryRepo_Debit_Idempotent(t *testing.T) {
	repo := seedRepo(10000)
	ctx := context.Background()

	// First debit.
	result1, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 3000)
	if err != nil {
		t.Fatalf("first debit: %v", err)
	}

	// Second debit with same transaction_id and source_step should be idempotent.
	result2, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 3000)
	if err != nil {
		t.Fatalf("second debit: %v", err)
	}

	// The balance should not have been debited twice.
	if result2.Wallet.Balance != 7000 {
		t.Errorf("balance after idempotent debit = %d, want 7000", result2.Wallet.Balance)
	}

	// The movement IDs should match.
	if result1.Movement.ID != result2.Movement.ID {
		t.Errorf("movement IDs differ: %s vs %s", result1.Movement.ID, result2.Movement.ID)
	}

	// Only one movement should exist.
	if len(repo.Movements) != 1 {
		t.Errorf("expected 1 movement, got %d", len(repo.Movements))
	}
}

func TestMemoryRepo_Debit_IdempotentReturnsOriginalBalanceAfter(t *testing.T) {
	repo := seedRepo(10000)
	ctx := context.Background()

	first, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 3000)
	if err != nil {
		t.Fatalf("first debit: %v", err)
	}
	if _, err := repo.Credit(ctx, "user-1", "txn-2", "deposit_credit", 1500); err != nil {
		t.Fatalf("credit after debit: %v", err)
	}

	duplicate, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 3000)
	if err != nil {
		t.Fatalf("duplicate debit: %v", err)
	}

	if duplicate.Movement.BalanceAfter != first.Movement.BalanceAfter {
		t.Fatalf("movement balance_after = %d, want %d", duplicate.Movement.BalanceAfter, first.Movement.BalanceAfter)
	}
	if duplicate.Wallet.Balance != first.Movement.BalanceAfter {
		t.Fatalf("wallet balance = %d, want %d", duplicate.Wallet.Balance, first.Movement.BalanceAfter)
	}
}

func TestMemoryRepo_Debit_DifferentSourceSteps(t *testing.T) {
	repo := seedRepo(10000)
	ctx := context.Background()

	// Same transaction, different source steps are NOT duplicates.
	_, err := repo.Debit(ctx, "user-1", "txn-1", "step_a", 2000)
	if err != nil {
		t.Fatalf("first debit: %v", err)
	}

	_, err = repo.Debit(ctx, "user-1", "txn-1", "step_b", 3000)
	if err != nil {
		t.Fatalf("second debit: %v", err)
	}

	// Balance should reflect both debits.
	w, _ := repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 5000 {
		t.Errorf("balance = %d, want 5000", w.Balance)
	}

	if len(repo.Movements) != 2 {
		t.Errorf("expected 2 movements, got %d", len(repo.Movements))
	}
}

// --- Credit tests ---

func TestMemoryRepo_Credit_Success(t *testing.T) {
	repo := seedRepo(5000)
	ctx := context.Background()

	result, err := repo.Credit(ctx, "user-1", "txn-1", "deposit_credit", 3000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Wallet.Balance != 8000 {
		t.Errorf("balance = %d, want 8000", result.Wallet.Balance)
	}
	if result.Movement.Type != MovementTypeCredit {
		t.Errorf("movement type = %v, want credit", result.Movement.Type)
	}
	if result.Movement.Amount != 3000 {
		t.Errorf("movement amount = %d, want 3000", result.Movement.Amount)
	}
	if result.Movement.BalanceBefore != 5000 {
		t.Errorf("balance_before = %d, want 5000", result.Movement.BalanceBefore)
	}
	if result.Movement.BalanceAfter != 8000 {
		t.Errorf("balance_after = %d, want 8000", result.Movement.BalanceAfter)
	}
}

func TestMemoryRepo_Credit_WalletNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	_, err := repo.Credit(ctx, "nonexistent", "txn-1", "deposit_credit", 1000)
	if !errors.Is(err, ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestMemoryRepo_Credit_Idempotent(t *testing.T) {
	repo := seedRepo(5000)
	ctx := context.Background()

	// First credit.
	result1, err := repo.Credit(ctx, "user-1", "txn-1", "deposit_credit", 3000)
	if err != nil {
		t.Fatalf("first credit: %v", err)
	}

	// Second credit with same transaction_id and source_step.
	result2, err := repo.Credit(ctx, "user-1", "txn-1", "deposit_credit", 3000)
	if err != nil {
		t.Fatalf("second credit: %v", err)
	}

	// Balance should NOT have been credited twice.
	if result2.Wallet.Balance != 8000 {
		t.Errorf("balance after idempotent credit = %d, want 8000", result2.Wallet.Balance)
	}

	if result1.Movement.ID != result2.Movement.ID {
		t.Errorf("movement IDs differ: %s vs %s", result1.Movement.ID, result2.Movement.ID)
	}

	if len(repo.Movements) != 1 {
		t.Errorf("expected 1 movement, got %d", len(repo.Movements))
	}
}

func TestMemoryRepo_Credit_IdempotentReturnsOriginalBalanceAfter(t *testing.T) {
	repo := seedRepo(5000)
	ctx := context.Background()

	first, err := repo.Credit(ctx, "user-1", "txn-1", "deposit_credit", 3000)
	if err != nil {
		t.Fatalf("first credit: %v", err)
	}
	if _, err := repo.Debit(ctx, "user-1", "txn-2", "purchase_debit", 1000); err != nil {
		t.Fatalf("debit after credit: %v", err)
	}

	duplicate, err := repo.Credit(ctx, "user-1", "txn-1", "deposit_credit", 3000)
	if err != nil {
		t.Fatalf("duplicate credit: %v", err)
	}

	if duplicate.Movement.BalanceAfter != first.Movement.BalanceAfter {
		t.Fatalf("movement balance_after = %d, want %d", duplicate.Movement.BalanceAfter, first.Movement.BalanceAfter)
	}
	if duplicate.Wallet.Balance != first.Movement.BalanceAfter {
		t.Fatalf("wallet balance = %d, want %d", duplicate.Wallet.Balance, first.Movement.BalanceAfter)
	}
}

// --- Atomic balance + movement persistence ---

func TestMemoryRepo_AtomicBalanceAndMovement(t *testing.T) {
	repo := seedRepo(10000)
	ctx := context.Background()

	// Perform a debit.
	result, err := repo.Debit(ctx, "user-1", "txn-1", "purchase_debit", 4000)
	if err != nil {
		t.Fatalf("debit: %v", err)
	}

	// The wallet balance and movement should be consistent.
	w, _ := repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 6000 {
		t.Errorf("wallet balance = %d, want 6000", w.Balance)
	}
	if result.Movement.BalanceAfter != w.Balance {
		t.Errorf("movement balance_after (%d) != wallet balance (%d)", result.Movement.BalanceAfter, w.Balance)
	}
	if result.Movement.BalanceBefore-result.Movement.Amount != result.Movement.BalanceAfter {
		t.Error("balance_before - amount != balance_after")
	}

	// Perform a credit.
	creditResult, err := repo.Credit(ctx, "user-1", "txn-2", "refund_credit", 2000)
	if err != nil {
		t.Fatalf("credit: %v", err)
	}

	w, _ = repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 8000 {
		t.Errorf("wallet balance after credit = %d, want 8000", w.Balance)
	}
	if creditResult.Movement.BalanceAfter != w.Balance {
		t.Errorf("credit movement balance_after (%d) != wallet balance (%d)", creditResult.Movement.BalanceAfter, w.Balance)
	}
	if creditResult.Movement.BalanceBefore+creditResult.Movement.Amount != creditResult.Movement.BalanceAfter {
		t.Error("balance_before + amount != balance_after for credit")
	}

	// Verify total movements.
	if len(repo.Movements) != 2 {
		t.Errorf("expected 2 movements, got %d", len(repo.Movements))
	}
}

// --- Concurrency safety ---

func TestMemoryRepo_ConcurrentDebits(t *testing.T) {
	repo := seedRepo(10000)
	ctx := context.Background()

	// Launch 10 concurrent debits of 1000 each. All should succeed since
	// total is exactly the balance.
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.Debit(ctx, "user-1", fmt.Sprintf("txn-%d", i), "concurrent_debit", 1000)
		}()
	}
	wg.Wait()

	// Count successes and insufficient funds errors.
	successes := 0
	insufficientFunds := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInsufficientFunds) {
			insufficientFunds++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}

	// All 10 should succeed since they lock sequentially.
	if successes != 10 {
		t.Errorf("expected 10 successes, got %d (insufficient: %d)", successes, insufficientFunds)
	}

	// Final balance should be 0.
	w, _ := repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 0 {
		t.Errorf("final balance = %d, want 0", w.Balance)
	}
}

func TestMemoryRepo_ConcurrentDebits_InsufficientFunds(t *testing.T) {
	repo := seedRepo(5000)
	ctx := context.Background()

	// Launch 10 concurrent debits of 1000 each. Only 5 should succeed.
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.Debit(ctx, "user-1", fmt.Sprintf("txn-%d", i), "concurrent_debit", 1000)
		}()
	}
	wg.Wait()

	successes := 0
	insufficientFunds := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInsufficientFunds) {
			insufficientFunds++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}

	if successes != 5 {
		t.Errorf("expected 5 successes, got %d", successes)
	}
	if insufficientFunds != 5 {
		t.Errorf("expected 5 insufficient funds, got %d", insufficientFunds)
	}

	w, _ := repo.GetWalletByUserID(ctx, "user-1")
	if w.Balance != 0 {
		t.Errorf("final balance = %d, want 0", w.Balance)
	}
}

// --- GetWalletByUserID ---

func TestMemoryRepo_GetWalletByUserID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.GetWalletByUserID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestMemoryRepo_GetWalletByUserID_Success(t *testing.T) {
	repo := seedRepo(5000)
	w, err := repo.GetWalletByUserID(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Balance != 5000 {
		t.Errorf("balance = %d, want 5000", w.Balance)
	}
	if w.Currency != "ARS" {
		t.Errorf("currency = %v, want ARS", w.Currency)
	}
}
