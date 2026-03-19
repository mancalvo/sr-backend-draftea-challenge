package saga

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMemoryRepository_CreateSaga(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	saga, err := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		Payload:       json.RawMessage(`{"user_id":"u1"}`),
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}
	if saga.ID == "" {
		t.Error("expected non-empty ID")
	}
	if saga.TransactionID != "txn-1" {
		t.Errorf("transaction_id = %s, want txn-1", saga.TransactionID)
	}
	if saga.Status != StatusCreated {
		t.Errorf("status = %s, want created", saga.Status)
	}
}

func TestMemoryRepository_DuplicateTransaction(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	_, err := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
	})
	if err != ErrDuplicateTransaction {
		t.Fatalf("expected ErrDuplicateTransaction, got %v", err)
	}
}

func TestMemoryRepository_GetSagaByID(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	created, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
	})

	got, err := repo.GetSagaByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if got.TransactionID != "txn-1" {
		t.Errorf("transaction_id = %s, want txn-1", got.TransactionID)
	}
}

func TestMemoryRepository_GetSagaByID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.GetSagaByID(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepository_GetSagaByTransactionID(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypePurchase,
	})

	got, err := repo.GetSagaByTransactionID(ctx, "txn-1")
	if err != nil {
		t.Fatalf("get saga by txn: %v", err)
	}
	if got.Type != SagaTypePurchase {
		t.Errorf("type = %s, want purchase", got.Type)
	}
}

func TestMemoryRepository_UpdateSagaStatus_LegalTransition(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	created, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
	})

	step := "deposit_start"
	updated, err := repo.UpdateSagaStatus(ctx, created.ID, StatusRunning, nil, &step)
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if updated.Status != StatusRunning {
		t.Errorf("status = %s, want running", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != "deposit_start" {
		t.Errorf("current_step = %v, want deposit_start", updated.CurrentStep)
	}
}

func TestMemoryRepository_UpdateSagaStatus_IllegalTransition(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	created, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
	})

	// created -> completed is NOT legal (must go through running first).
	outcome := OutcomeSucceeded
	_, err := repo.UpdateSagaStatus(ctx, created.ID, StatusCompleted, &outcome, nil)
	if err == nil {
		t.Fatal("expected error for illegal transition created -> completed")
	}
}

func TestMemoryRepository_UpdateSagaStatus_WithOutcome(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	created, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
	})

	// created -> running.
	repo.UpdateSagaStatus(ctx, created.ID, StatusRunning, nil, nil)

	// running -> completed with outcome.
	outcome := OutcomeSucceeded
	updated, err := repo.UpdateSagaStatus(ctx, created.ID, StatusCompleted, &outcome, nil)
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if updated.Outcome == nil || *updated.Outcome != OutcomeSucceeded {
		t.Errorf("outcome = %v, want succeeded", updated.Outcome)
	}
}

func TestMemoryRepository_UpdateSagaStatus_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.UpdateSagaStatus(context.Background(), "nonexistent", StatusRunning, nil, nil)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepository_ListTimedOutSagas(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	past := time.Now().UTC().Add(-1 * time.Minute)
	future := time.Now().UTC().Add(1 * time.Hour)

	// Saga with past timeout (should be returned).
	repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		TimeoutAt:     &past,
	})

	// Saga with future timeout (should not be returned).
	repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-2",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		TimeoutAt:     &future,
	})

	// Saga already completed (should not be returned).
	s3, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-3",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		TimeoutAt:     &past,
	})
	repo.UpdateSagaStatus(ctx, s3.ID, StatusRunning, nil, nil)
	outcome := OutcomeSucceeded
	repo.UpdateSagaStatus(ctx, s3.ID, StatusCompleted, &outcome, nil)

	timedOut, err := repo.ListTimedOutSagas(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("list timed out: %v", err)
	}
	if len(timedOut) != 1 {
		t.Fatalf("expected 1 timed-out saga, got %d", len(timedOut))
	}
	if timedOut[0].TransactionID != "txn-1" {
		t.Errorf("expected txn-1, got %s", timedOut[0].TransactionID)
	}
}

func TestMemoryRepository_IdempotencyKey_SaveAndGet(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	ik := &IdempotencyKey{
		Key:            "test-key",
		Scope:          "deposit",
		RequestHash:    "abc123",
		TransactionID:  "txn-1",
		ResponseStatus: 202,
		ResponseBody:   json.RawMessage(`{"success":true}`),
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}

	if err := repo.SaveIdempotencyKey(ctx, ik); err != nil {
		t.Fatalf("save key: %v", err)
	}

	got, err := repo.GetIdempotencyKey(ctx, "deposit", "test-key")
	if err != nil {
		t.Fatalf("get key: %v", err)
	}
	if got.TransactionID != "txn-1" {
		t.Errorf("transaction_id = %s, want txn-1", got.TransactionID)
	}
	if got.ResponseStatus != 202 {
		t.Errorf("response_status = %d, want 202", got.ResponseStatus)
	}
	if got.Scope != "deposit" {
		t.Errorf("scope = %s, want deposit", got.Scope)
	}
	if got.RequestHash != "abc123" {
		t.Errorf("request_hash = %s, want abc123", got.RequestHash)
	}
}

func TestMemoryRepository_IdempotencyKey_Duplicate(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	ik := &IdempotencyKey{
		Key:            "dup-key",
		Scope:          "deposit",
		RequestHash:    "hash1",
		TransactionID:  "txn-1",
		ResponseStatus: 202,
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}

	repo.SaveIdempotencyKey(ctx, ik)
	err := repo.SaveIdempotencyKey(ctx, ik)
	if err != ErrIdempotencyKeyExists {
		t.Fatalf("expected ErrIdempotencyKeyExists, got %v", err)
	}
}

func TestMemoryRepository_IdempotencyKey_Expired(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	ik := &IdempotencyKey{
		Key:            "expired-key",
		Scope:          "deposit",
		RequestHash:    "hash1",
		TransactionID:  "txn-1",
		ResponseStatus: 202,
		ExpiresAt:      time.Now().UTC().Add(-1 * time.Hour), // already expired
	}

	repo.SaveIdempotencyKey(ctx, ik)

	_, err := repo.GetIdempotencyKey(ctx, "deposit", "expired-key")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for expired key, got %v", err)
	}
}

func TestMemoryRepository_IdempotencyKey_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.GetIdempotencyKey(context.Background(), "deposit", "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepository_IdempotencyKey_ScopeIsolation(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Save a key under "deposit" scope.
	ik1 := &IdempotencyKey{
		Key:            "shared-key",
		Scope:          "deposit",
		RequestHash:    "hash-deposit",
		TransactionID:  "txn-1",
		ResponseStatus: 202,
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}
	if err := repo.SaveIdempotencyKey(ctx, ik1); err != nil {
		t.Fatalf("save deposit key: %v", err)
	}

	// Save the same key under "purchase" scope -> should succeed (different scope).
	ik2 := &IdempotencyKey{
		Key:            "shared-key",
		Scope:          "purchase",
		RequestHash:    "hash-purchase",
		TransactionID:  "txn-2",
		ResponseStatus: 202,
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}
	if err := repo.SaveIdempotencyKey(ctx, ik2); err != nil {
		t.Fatalf("save purchase key: %v", err)
	}

	// Lookup by deposit scope.
	got, err := repo.GetIdempotencyKey(ctx, "deposit", "shared-key")
	if err != nil {
		t.Fatalf("get deposit key: %v", err)
	}
	if got.TransactionID != "txn-1" {
		t.Errorf("deposit: transaction_id = %s, want txn-1", got.TransactionID)
	}

	// Lookup by purchase scope.
	got, err = repo.GetIdempotencyKey(ctx, "purchase", "shared-key")
	if err != nil {
		t.Fatalf("get purchase key: %v", err)
	}
	if got.TransactionID != "txn-2" {
		t.Errorf("purchase: transaction_id = %s, want txn-2", got.TransactionID)
	}

	// Lookup by a scope that was never used -> not found.
	_, err = repo.GetIdempotencyKey(ctx, "refund", "shared-key")
	if err != ErrNotFound {
		t.Fatalf("refund scope: expected ErrNotFound, got %v", err)
	}
}
