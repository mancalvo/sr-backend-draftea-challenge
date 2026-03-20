package saga

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestTimeoutPoller_Poll_TimesOutRunningSagas(t *testing.T) {
	repo := NewMemoryRepository()
	payments := &recordingPaymentsClient{}
	poller := NewTimeoutPoller(repo, payments, DefaultTimeoutConfig(), discardLogger())

	past := time.Now().UTC().Add(-1 * time.Minute)
	payload, _ := json.Marshal(DepositPayload{
		UserID:   "user-1",
		Amount:   10000,
		Currency: "ARS",
	})

	s, err := repo.CreateSaga(context.Background(), &SagaInstance{
		TransactionID: "txn-timeout-running",
		Type:          SagaTypeDeposit,
		Status:        StatusRunning,
		CurrentStep:   stringPtr("deposit_charge"),
		Payload:       payload,
		TimeoutAt:     &past,
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}

	poller.poll(context.Background())

	got, err := repo.GetSagaByID(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if got.Status != StatusTimedOut {
		t.Fatalf("status = %s, want timed_out", got.Status)
	}
	updates := payments.Updates()
	if len(updates) != 1 {
		t.Fatalf("payments updates = %d, want 1", len(updates))
	}
	if updates[0].TransactionID != "txn-timeout-running" {
		t.Fatalf("transaction_id = %s, want txn-timeout-running", updates[0].TransactionID)
	}
	if updates[0].Status != "timed_out" {
		t.Fatalf("status = %s, want timed_out", updates[0].Status)
	}
}

func TestTimeoutPoller_Poll_RetriesLedgerBeforeMarkingTimedOut(t *testing.T) {
	repo := NewMemoryRepository()
	payments := &failingPaymentsClient{failCount: 1}
	poller := NewTimeoutPoller(repo, payments, DefaultTimeoutConfig(), discardLogger())

	past := time.Now().UTC().Add(-1 * time.Minute)
	payload, _ := json.Marshal(DepositPayload{
		UserID:   "user-1",
		Amount:   10000,
		Currency: "ARS",
	})

	s, err := repo.CreateSaga(context.Background(), &SagaInstance{
		TransactionID: "txn-timeout-retry",
		Type:          SagaTypeDeposit,
		Status:        StatusRunning,
		CurrentStep:   stringPtr("deposit_charge"),
		Payload:       payload,
		TimeoutAt:     &past,
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}

	poller.poll(context.Background())

	first, err := repo.GetSagaByID(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("get saga after first poll: %v", err)
	}
	if first.Status != StatusRunning {
		t.Fatalf("status after first poll = %s, want running", first.Status)
	}

	poller.poll(context.Background())

	second, err := repo.GetSagaByID(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("get saga after second poll: %v", err)
	}
	if second.Status != StatusTimedOut {
		t.Fatalf("status after second poll = %s, want timed_out", second.Status)
	}
	if len(payments.Updates()) != 1 {
		t.Fatalf("payments updates = %d, want 1", len(payments.Updates()))
	}
}

func TestTimeoutPoller_Poll_IgnoresCreatedSagas(t *testing.T) {
	repo := NewMemoryRepository()
	payments := &recordingPaymentsClient{}
	poller := NewTimeoutPoller(repo, payments, DefaultTimeoutConfig(), discardLogger())

	past := time.Now().UTC().Add(-1 * time.Minute)
	payload, _ := json.Marshal(DepositPayload{
		UserID:   "user-1",
		Amount:   10000,
		Currency: "ARS",
	})

	s, err := repo.CreateSaga(context.Background(), &SagaInstance{
		TransactionID: "txn-timeout-created",
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &past,
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}

	poller.poll(context.Background())

	got, err := repo.GetSagaByID(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if got.Status != StatusCreated {
		t.Fatalf("status = %s, want created", got.Status)
	}
	if len(payments.Updates()) != 0 {
		t.Fatalf("payments updates = %d, want 0", len(payments.Updates()))
	}
}

func stringPtr(v string) *string {
	return &v
}
