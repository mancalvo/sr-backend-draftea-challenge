package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
)

type (
	IdempotencyKey = domain.IdempotencyKey
	SagaInstance   = domain.SagaInstance
)

const (
	OutcomeSucceeded           = domain.OutcomeSucceeded
	IdempotencyStateCompleted  = domain.IdempotencyStateCompleted
	IdempotencyStateProcessing = domain.IdempotencyStateProcessing

	SagaTypeDeposit  = domain.SagaTypeDeposit
	SagaTypePurchase = domain.SagaTypePurchase

	StatusCompleted = domain.StatusCompleted
	StatusCreated   = domain.StatusCreated
	StatusRunning   = domain.StatusRunning
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

func TestMemoryRepository_UpdateSagaStep(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	saga, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-1",
		Type:          SagaTypeDeposit,
		Status:        StatusRunning,
	})

	step := "deposit_credit"
	updated, err := repo.UpdateSagaStep(ctx, saga.ID, &step)
	if err != nil {
		t.Fatalf("update saga step: %v", err)
	}
	if updated.Status != StatusRunning {
		t.Fatalf("status = %s, want running", updated.Status)
	}
	if updated.CurrentStep == nil || *updated.CurrentStep != step {
		t.Fatalf("current_step = %v, want %s", updated.CurrentStep, step)
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
		Status:        StatusRunning,
		TimeoutAt:     &past,
	})

	// Saga with future timeout (should not be returned).
	repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-2",
		Type:          SagaTypeDeposit,
		Status:        StatusRunning,
		TimeoutAt:     &future,
	})

	// Saga still in created state (should not be returned).
	s3, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-3",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		TimeoutAt:     &past,
	})

	// Saga already completed (should not be returned).
	s4, _ := repo.CreateSaga(ctx, &SagaInstance{
		TransactionID: "txn-4",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		TimeoutAt:     &past,
	})
	repo.UpdateSagaStatus(ctx, s4.ID, StatusRunning, nil, nil)
	outcome := OutcomeSucceeded
	repo.UpdateSagaStatus(ctx, s4.ID, StatusCompleted, &outcome, nil)

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
	if s3.TransactionID == timedOut[0].TransactionID {
		t.Fatalf("created saga should not be eligible for timeout polling")
	}
}

func TestMemoryRepository_IdempotencyKey_ReserveAndFinalize(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	ik := &IdempotencyKey{
		Key:           "test-key",
		Scope:         "deposit",
		RequestHash:   "abc123",
		TransactionID: "txn-1",
	}

	reserved, claimed, err := repo.ReserveIdempotencyKey(ctx, ik, 30*time.Second)
	if err != nil {
		t.Fatalf("reserve key: %v", err)
	}
	if !claimed {
		t.Fatal("expected reservation to be claimed")
	}
	if reserved.TransactionID != "txn-1" {
		t.Fatalf("transaction_id = %s, want txn-1", reserved.TransactionID)
	}
	if reserved.State != IdempotencyStateProcessing {
		t.Fatalf("state = %s, want processing", reserved.State)
	}

	body := json.RawMessage(`{"success":true}`)
	if err := repo.FinalizeIdempotencyKey(ctx, "deposit", "test-key", 202, body, 24*time.Hour); err != nil {
		t.Fatalf("finalize key: %v", err)
	}

	got, claimed, err := repo.ReserveIdempotencyKey(ctx, ik, 30*time.Second)
	if err != nil {
		t.Fatalf("reserve completed key: %v", err)
	}
	if claimed {
		t.Fatal("expected completed key to replay instead of being claimed")
	}
	if got.TransactionID != "txn-1" {
		t.Errorf("transaction_id = %s, want txn-1", got.TransactionID)
	}
	if got.State != IdempotencyStateCompleted {
		t.Errorf("state = %s, want completed", got.State)
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
	if string(got.ResponseBody) != string(body) {
		t.Errorf("response_body = %s, want %s", got.ResponseBody, body)
	}
}

func TestMemoryRepository_IdempotencyKey_ActiveProcessingBlocksSecondClaim(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	ik := &IdempotencyKey{
		Key:           "dup-key",
		Scope:         "deposit",
		RequestHash:   "hash1",
		TransactionID: "txn-1",
	}

	_, claimed, err := repo.ReserveIdempotencyKey(ctx, ik, time.Minute)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if !claimed {
		t.Fatal("expected first reserve to claim key")
	}

	got, claimed, err := repo.ReserveIdempotencyKey(ctx, ik, time.Minute)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if claimed {
		t.Fatal("expected second reserve to observe active processing key")
	}
	if got.State != IdempotencyStateProcessing {
		t.Fatalf("state = %s, want processing", got.State)
	}
}

func TestMemoryRepository_IdempotencyKey_ExpiredProcessingReusesTransactionID(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	first := &IdempotencyKey{
		Key:           "expired-key",
		Scope:         "deposit",
		RequestHash:   "hash1",
		TransactionID: "txn-1",
	}

	_, claimed, err := repo.ReserveIdempotencyKey(ctx, first, time.Minute)
	if err != nil {
		t.Fatalf("initial reserve: %v", err)
	}
	if !claimed {
		t.Fatal("expected initial reserve to claim key")
	}
	if err := repo.MarkIdempotencyKeyRetryable(ctx, "deposit", "expired-key"); err != nil {
		t.Fatalf("mark retryable: %v", err)
	}

	second := &IdempotencyKey{
		Key:           "expired-key",
		Scope:         "deposit",
		RequestHash:   "hash1",
		TransactionID: "txn-2",
	}
	got, claimed, err := repo.ReserveIdempotencyKey(ctx, second, time.Minute)
	if err != nil {
		t.Fatalf("reclaim reserve: %v", err)
	}
	if !claimed {
		t.Fatal("expected expired processing key to be reclaimable")
	}
	if got.TransactionID != "txn-1" {
		t.Fatalf("transaction_id = %s, want original txn-1", got.TransactionID)
	}
}

func TestMemoryRepository_IdempotencyKey_ExpiredCompletedCanBeReclaimed(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	original := &IdempotencyKey{
		Key:           "shared-key",
		Scope:         "deposit",
		RequestHash:   "hash-deposit",
		TransactionID: "txn-1",
	}
	_, claimed, err := repo.ReserveIdempotencyKey(ctx, original, time.Minute)
	if err != nil {
		t.Fatalf("reserve original key: %v", err)
	}
	if !claimed {
		t.Fatal("expected original key to be claimed")
	}
	if err := repo.FinalizeIdempotencyKey(ctx, "deposit", "shared-key", 202, json.RawMessage(`{"success":true}`), time.Nanosecond); err != nil {
		t.Fatalf("finalize original key: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	reclaimed := &IdempotencyKey{
		Key:           "shared-key",
		Scope:         "deposit",
		RequestHash:   "hash-new",
		TransactionID: "txn-2",
	}
	got, claimed, err := repo.ReserveIdempotencyKey(ctx, reclaimed, time.Minute)
	if err != nil {
		t.Fatalf("reclaim completed key: %v", err)
	}
	if !claimed {
		t.Fatal("expected expired completed key to be reclaimable")
	}
	if got.TransactionID != "txn-2" {
		t.Fatalf("transaction_id = %s, want txn-2", got.TransactionID)
	}
	if got.RequestHash != "hash-new" {
		t.Fatalf("request_hash = %s, want hash-new", got.RequestHash)
	}
}
