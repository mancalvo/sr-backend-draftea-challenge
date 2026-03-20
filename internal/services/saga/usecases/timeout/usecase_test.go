package timeout

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/workflows"
)

type (
	DepositPayload = workflows.DepositPayload
	SagaInstance   = domain.SagaInstance
)

const (
	SagaTypeDeposit = domain.SagaTypeDeposit
	StatusCreated   = domain.StatusCreated
	StatusRunning   = domain.StatusRunning
	StatusTimedOut  = domain.StatusTimedOut
)

var NewMemoryRepository = repository.NewMemoryRepository

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type statusUpdate struct {
	TransactionID string
	Status        string
	Reason        *string
	ProviderRef   *string
}

type recordingPaymentsClient struct {
	mu      sync.Mutex
	updates []statusUpdate
}

func (c *recordingPaymentsClient) RegisterTransaction(_ context.Context, req client.RegisterTransactionRequest) (*client.RegisterTransactionResponse, error) {
	return &client.RegisterTransactionResponse{ID: req.ID, Status: "pending"}, nil
}

func (c *recordingPaymentsClient) GetTransaction(_ context.Context, transactionID string) (*client.TransactionDetails, error) {
	return &client.TransactionDetails{ID: transactionID}, nil
}

func (c *recordingPaymentsClient) UpdateTransactionStatus(_ context.Context, transactionID string, status string, reason *string, providerReference *string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, statusUpdate{
		TransactionID: transactionID,
		Status:        status,
		Reason:        reason,
		ProviderRef:   providerReference,
	})
	return nil
}

func (c *recordingPaymentsClient) Updates() []statusUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]statusUpdate, len(c.updates))
	copy(result, c.updates)
	return result
}

type failingPaymentsClient struct {
	recordingPaymentsClient
	failCount int
	calls     int
}

func (c *failingPaymentsClient) UpdateTransactionStatus(ctx context.Context, transactionID string, status string, reason *string, providerReference *string) error {
	c.calls++
	if c.calls <= c.failCount {
		return errors.New("temporary failure")
	}
	return c.recordingPaymentsClient.UpdateTransactionStatus(ctx, transactionID, status, reason, providerReference)
}

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
