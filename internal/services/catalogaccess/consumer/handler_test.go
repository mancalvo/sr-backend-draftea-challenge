package consumer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
	catalogservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/service"
)

var NewMemoryRepository = repository.NewMemoryRepository

type (
	MemoryRepository = repository.MemoryRepository
	User             = domain.User
	Offering         = domain.Offering
	AccessRecord     = domain.AccessRecord
)

const (
	AccessStatusActive  = domain.AccessStatusActive
	AccessStatusRevoked = domain.AccessStatusRevoked
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func NewConsumerHandler(repo catalogservice.Repository, publisher Publisher, logger *slog.Logger) *Handler {
	return NewHandler(catalogservice.New(repo), publisher, logger)
}

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

// mockPublisher records publish calls for test verification.
type mockPublisher struct {
	calls []publishCall
}

type publishCall struct {
	Exchange      string
	RoutingKey    string
	CorrelationID string
	Payload       any
}

func (m *mockPublisher) Publish(_ context.Context, exchange, routingKey, correlationID string, payload any) error {
	m.calls = append(m.calls, publishCall{
		Exchange:      exchange,
		RoutingKey:    routingKey,
		CorrelationID: correlationID,
		Payload:       payload,
	})
	return nil
}

func makeEnvelope(t *testing.T, msgType string, payload any) messaging.Envelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return messaging.Envelope{
		MessageID:     "msg-test",
		CorrelationID: "corr-test",
		Type:          msgType,
		Timestamp:     time.Now().UTC(),
		Payload:       json.RawMessage(data),
	}
}

// --- access.grant.requested ---

func TestHandleAccessGrantRequested_Success(t *testing.T) {
	repo := seedRepo(false) // no existing access
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.AccessGrantRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		OfferingID:    "offering-1",
	}
	env := makeEnvelope(t, messaging.RoutingKeyAccessGrantRequested, cmd)

	err := ch.Handle(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify access was created.
	if len(repo.AccessRecords) != 1 {
		t.Fatalf("expected 1 access record, got %d", len(repo.AccessRecords))
	}
	ar := repo.AccessRecords[0]
	if ar.UserID != "user-1" || ar.OfferingID != "offering-1" || ar.TransactionID != "txn-1" {
		t.Errorf("access record mismatch: %+v", ar)
	}
	if ar.Status != AccessStatusActive {
		t.Errorf("status = %v, want active", ar.Status)
	}

	// Verify outcome published.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyAccessGranted {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyAccessGranted)
	}
	if call.Exchange != messaging.ExchangeOutcomes {
		t.Errorf("exchange = %q, want %q", call.Exchange, messaging.ExchangeOutcomes)
	}
	if call.CorrelationID != "corr-test" {
		t.Errorf("correlation_id = %q, want corr-test", call.CorrelationID)
	}
}

func TestHandle_UnknownMessageType_Ignored(t *testing.T) {
	repo := seedRepo(false)
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	env := makeEnvelope(t, "access.unknown", map[string]string{"status": "noop"})
	if err := ch.Handle(context.Background(), env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.calls) != 0 {
		t.Fatalf("publish calls = %d, want 0", len(pub.calls))
	}
}

func TestHandleAccessGrantRequested_DuplicateAccess(t *testing.T) {
	repo := seedRepo(true) // user-1 already has access to offering-1
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.AccessGrantRequested{
		TransactionID: "txn-2",
		UserID:        "user-1",
		OfferingID:    "offering-1",
	}
	env := makeEnvelope(t, messaging.RoutingKeyAccessGrantRequested, cmd)

	err := ch.HandleAccessGrantRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT create a second access record.
	activeCount := 0
	for _, ar := range repo.AccessRecords {
		if ar.Status == AccessStatusActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("expected 1 active access record, got %d", activeCount)
	}

	// Should publish conflicted outcome.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyAccessGrantConflicted {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyAccessGrantConflicted)
	}
}

func TestHandleAccessGrantRequested_DuplicateRedelivery_ReplaysGranted(t *testing.T) {
	repo := seedRepo(true) // existing access belongs to txn-original
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.AccessGrantRequested{
		TransactionID: "txn-original",
		UserID:        "user-1",
		OfferingID:    "offering-1",
	}
	env := makeEnvelope(t, messaging.RoutingKeyAccessGrantRequested, cmd)

	err := ch.HandleAccessGrantRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	if pub.calls[0].RoutingKey != messaging.RoutingKeyAccessGranted {
		t.Errorf("routing key = %q, want %q", pub.calls[0].RoutingKey, messaging.RoutingKeyAccessGranted)
	}
}

// --- access.revoke.requested ---

func TestHandleAccessRevokeRequested_Success(t *testing.T) {
	repo := seedRepo(true) // has active access for txn-original
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.AccessRevokeRequested{
		TransactionID:         "txn-refund",
		OriginalTransactionID: "txn-original",
		UserID:                "user-1",
		OfferingID:            "offering-1",
	}
	env := makeEnvelope(t, messaging.RoutingKeyAccessRevokeRequested, cmd)

	err := ch.HandleAccessRevokeRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify access was revoked.
	ar := repo.AccessRecords[0]
	if ar.Status != AccessStatusRevoked {
		t.Errorf("status = %v, want revoked", ar.Status)
	}
	if ar.RevokedAt == nil {
		t.Error("revoked_at should be set")
	}

	// Verify outcome published.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyAccessRevoked {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyAccessRevoked)
	}
	payload, ok := call.Payload.(messaging.AccessRevoked)
	if !ok {
		t.Fatalf("expected AccessRevoked payload, got %T", call.Payload)
	}
	if payload.TransactionID != "txn-refund" {
		t.Errorf("transaction_id = %q, want %q", payload.TransactionID, "txn-refund")
	}
	if payload.OriginalTransactionID != "txn-original" {
		t.Errorf("original_transaction_id = %q, want %q", payload.OriginalTransactionID, "txn-original")
	}
}

func TestHandleAccessRevokeRequested_NoActiveAccess(t *testing.T) {
	repo := seedRepo(false) // no access records
	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.AccessRevokeRequested{
		TransactionID:         "txn-refund",
		OriginalTransactionID: "txn-nonexistent",
		UserID:                "user-1",
		OfferingID:            "offering-1",
	}
	env := makeEnvelope(t, messaging.RoutingKeyAccessRevokeRequested, cmd)

	err := ch.HandleAccessRevokeRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should publish revoke rejected outcome.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.RoutingKey != messaging.RoutingKeyAccessRevokeRejected {
		t.Errorf("routing key = %q, want %q", call.RoutingKey, messaging.RoutingKeyAccessRevokeRejected)
	}
	payload, ok := call.Payload.(messaging.AccessRevokeRejected)
	if !ok {
		t.Fatalf("expected AccessRevokeRejected payload, got %T", call.Payload)
	}
	if payload.TransactionID != "txn-refund" {
		t.Errorf("transaction_id = %q, want %q", payload.TransactionID, "txn-refund")
	}
	if payload.OriginalTransactionID != "txn-nonexistent" {
		t.Errorf("original_transaction_id = %q, want %q", payload.OriginalTransactionID, "txn-nonexistent")
	}
}

func TestHandleAccessRevokeRequested_AlreadyRevoked(t *testing.T) {
	repo := seedRepo(true)
	// Manually revoke the access record first.
	now := time.Now().UTC()
	repo.AccessRecords[0].Status = AccessStatusRevoked
	repo.AccessRecords[0].RevokedAt = &now

	pub := &mockPublisher{}
	ch := NewConsumerHandler(repo, pub, testLogger())

	cmd := messaging.AccessRevokeRequested{
		TransactionID:         "txn-refund",
		OriginalTransactionID: "txn-original",
		UserID:                "user-1",
		OfferingID:            "offering-1",
	}
	env := makeEnvelope(t, messaging.RoutingKeyAccessRevokeRequested, cmd)

	err := ch.HandleAccessRevokeRequested(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Duplicate redelivery after a successful revoke should replay the success outcome.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	if pub.calls[0].RoutingKey != messaging.RoutingKeyAccessRevoked {
		t.Errorf("routing key = %q, want %q", pub.calls[0].RoutingKey, messaging.RoutingKeyAccessRevoked)
	}
	payload, ok := pub.calls[0].Payload.(messaging.AccessRevoked)
	if !ok {
		t.Fatalf("expected AccessRevoked payload, got %T", pub.calls[0].Payload)
	}
	if payload.TransactionID != "txn-refund" {
		t.Errorf("transaction_id = %q, want %q", payload.TransactionID, "txn-refund")
	}
	if payload.OriginalTransactionID != "txn-original" {
		t.Errorf("original_transaction_id = %q, want %q", payload.OriginalTransactionID, "txn-original")
	}
}
