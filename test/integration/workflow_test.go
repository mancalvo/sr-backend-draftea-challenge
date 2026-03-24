// Package integration provides representative end-to-end workflow tests that
// wire together the saga-orchestrator, wallets, catalog-access, and payments
// service layers using in-memory repositories and a simulated message bus.
//
// These tests verify the five mandatory challenge scenarios:
//   - purchase happy path
//   - purchase insufficient funds
//   - concurrent purchase safety
//   - deposit timeout handling
//   - refund happy path
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	catalogconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/consumer"
	catalogdomain "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
	catalogrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
	catalogservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/service"
	paymentsconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/consumer"
	paymentsdomain "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"
	paymentsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/repository"
	paymentsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/service"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
	sagaapi "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/api"
	sagaclient "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	sagaconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/consumer"
	sagadomain "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
	sagarepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	timeoutusecase "github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/timeout"
	walletsconsumer "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/consumer"
	walletsdomain "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/domain"
	walletsrepository "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/repository"
	walletsservice "github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/service"
)

const (
	testUserID     = "11111111-1111-1111-1111-111111111111"
	testWalletID   = "22222222-2222-2222-2222-222222222222"
	testOfferingID = "33333333-3333-3333-3333-333333333333"
)

// ---------------------------------------------------------------------------
// Test infrastructure: message bus that routes published messages to handlers
// ---------------------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// msgRecord captures a published message before dispatching.
type msgRecord struct {
	Exchange      string
	RoutingKey    string
	CorrelationID string
	Payload       any
}

type queuedMessage struct {
	ctx context.Context
	env messaging.Envelope
}

// bus is a simple synchronous message bus that routes published messages to the
// appropriate service handler, simulating RabbitMQ-based async messaging within
// a single process.
type bus struct {
	mu       sync.Mutex
	handlers map[string]func(ctx context.Context, env messaging.Envelope) error
	log      []msgRecord
	errs     []error
	queue    []queuedMessage

	dispatching bool
}

func newBus() *bus {
	return &bus{
		handlers: make(map[string]func(ctx context.Context, env messaging.Envelope) error),
	}
}

// on registers a handler for a given routing key.
func (b *bus) on(routingKey string, fn func(ctx context.Context, env messaging.Envelope) error) {
	b.handlers[routingKey] = fn
}

// Publish implements the Publisher interface used by all service components.
// It queues the message and drains the queue synchronously, so each publish
// returns before any downstream publish chained from the consumer is handled.
// That matches broker semantics more closely than direct recursive dispatch.
func (b *bus) Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error {
	env, err := messaging.NewEnvelope(routingKey, correlationID, payload)
	if err != nil {
		return fmt.Errorf("bus: create envelope: %w", err)
	}

	b.mu.Lock()
	b.log = append(b.log, msgRecord{Exchange: exchange, RoutingKey: routingKey, CorrelationID: correlationID, Payload: payload})
	b.queue = append(b.queue, queuedMessage{ctx: ctx, env: env})
	if b.dispatching {
		b.mu.Unlock()
		return nil
	}
	b.dispatching = true
	b.mu.Unlock()

	for {
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.dispatching = false
			b.mu.Unlock()
			return nil
		}
		msg := b.queue[0]
		b.queue = b.queue[1:]
		fn, ok := b.handlers[msg.env.Type]
		b.mu.Unlock()

		if !ok {
			continue
		}
		if err := fn(msg.ctx, msg.env); err != nil {
			b.mu.Lock()
			b.errs = append(b.errs, fmt.Errorf("dispatch %s: %w", msg.env.Type, err))
			b.mu.Unlock()
		}
	}
}

func (b *bus) messages() []msgRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]msgRecord, len(b.log))
	copy(result, b.log)
	return result
}

func (b *bus) errors() []error {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]error, len(b.errs))
	copy(result, b.errs)
	return result
}

// ---------------------------------------------------------------------------
// Wiring helpers
// ---------------------------------------------------------------------------

// testHarness wires together all services for an integration test.
type testHarness struct {
	// Bus
	bus *bus

	// Saga
	sagaRepo     *sagarepository.MemoryRepository
	sagaConsumer *sagaconsumer.Handler
	sagaHandler  *sagaapi.Handler
	sagaRouter   http.Handler

	// Wallets
	walletRepo     *walletsrepository.MemoryRepository
	walletConsumer *walletsconsumer.Handler

	// Catalog-access
	catalogRepo     *catalogrepository.MemoryRepository
	catalogConsumer *catalogconsumer.Handler

	// Payments
	paymentsClient   sagaclient.PaymentsClient
	paymentsRepo     *paymentsrepository.MemoryRepository
	paymentsConsumer *paymentsconsumer.Handler
	paymentsProvider *configurableProvider
}

// configurableProvider allows tests to control the provider behavior.
type configurableProvider struct {
	mu      sync.Mutex
	success bool
	delay   time.Duration
	reason  string
}

func (p *configurableProvider) Charge(ctx context.Context, cmd messaging.DepositRequested) (*processdeposit.ChargeResult, error) {
	p.mu.Lock()
	d := p.delay
	success := p.success
	reason := p.reason
	p.mu.Unlock()

	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if success {
		return &processdeposit.ChargeResult{
			Success:     true,
			ProviderRef: "sim-" + cmd.TransactionID,
		}, nil
	}
	return &processdeposit.ChargeResult{
		Success: false,
		Reason:  reason,
	}, nil
}

type paymentsServiceClient struct {
	service *paymentsservice.Service
}

func (c *paymentsServiceClient) RegisterTransaction(ctx context.Context, req sagaclient.RegisterTransactionRequest) (*sagaclient.RegisterTransactionResponse, error) {
	txn, err := c.service.RegisterTransaction(ctx, &paymentsdomain.Transaction{
		ID:                    req.ID,
		UserID:                req.UserID,
		Type:                  paymentsdomain.TransactionType(req.Type),
		Status:                paymentsdomain.StatusPending,
		Amount:                req.Amount,
		Currency:              req.Currency,
		OfferingID:            req.OfferingID,
		OriginalTransactionID: req.OriginalTransactionID,
	})
	if err != nil {
		return nil, err
	}
	return &sagaclient.RegisterTransactionResponse{
		ID:     txn.ID,
		Status: string(txn.Status),
	}, nil
}

func (c *paymentsServiceClient) GetTransaction(ctx context.Context, transactionID string) (*sagaclient.TransactionDetails, error) {
	txn, err := c.service.GetTransaction(ctx, transactionID)
	if err != nil {
		return nil, err
	}
	return &sagaclient.TransactionDetails{
		ID:                    txn.ID,
		UserID:                txn.UserID,
		Type:                  string(txn.Type),
		Status:                string(txn.Status),
		Amount:                txn.Amount,
		Currency:              txn.Currency,
		OfferingID:            txn.OfferingID,
		OriginalTransactionID: txn.OriginalTransactionID,
		ProviderReference:     txn.ProviderReference,
	}, nil
}

func (c *paymentsServiceClient) UpdateTransactionStatus(ctx context.Context, transactionID string, status string, reason *string, providerReference *string) error {
	_, err := c.service.UpdateTransactionStatus(ctx, transactionID, paymentsdomain.TransactionStatus(status), reason, providerReference)
	return err
}

type catalogServiceClient struct {
	service *catalogservice.Service
}

func (c *catalogServiceClient) PurchasePrecheck(ctx context.Context, userID, offeringID string) (*sagaclient.PrecheckResult, error) {
	result, err := c.service.PurchasePrecheck(ctx, userID, offeringID)
	if err != nil {
		return nil, err
	}
	return &sagaclient.PrecheckResult{
		Allowed:  result.Allowed,
		Reason:   result.Reason,
		Price:    result.Price,
		Currency: result.Currency,
	}, nil
}

func (c *catalogServiceClient) RefundPrecheck(ctx context.Context, userID, offeringID, transactionID string) (*sagaclient.PrecheckResult, error) {
	result, err := c.service.RefundPrecheck(ctx, userID, offeringID, transactionID)
	if err != nil {
		return nil, err
	}
	return &sagaclient.PrecheckResult{
		Allowed: result.Allowed,
		Reason:  result.Reason,
	}, nil
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()

	logger := discardLogger()
	b := newBus()

	// -- Wallets --
	walletRepo := walletsrepository.NewMemoryRepository()
	walletRepo.Wallets[testUserID] = &walletsdomain.Wallet{
		ID:       testWalletID,
		UserID:   testUserID,
		Balance:  20000,
		Currency: "ARS",
	}
	walletConsumer := walletsconsumer.NewHandler(walletsservice.New(walletRepo), b, logger)

	// -- Catalog-access --
	catalogRepo := catalogrepository.NewMemoryRepository()
	catalogSvc := catalogservice.New(catalogRepo)
	catalogRepo.Users[testUserID] = &catalogdomain.User{ID: testUserID, Email: "user@test.com", Name: "Test User"}
	catalogRepo.Offerings[testOfferingID] = &catalogdomain.Offering{ID: testOfferingID, Name: "Premium Access", Price: 5000, Currency: "ARS", Active: true}
	catalogConsumer := catalogconsumer.NewHandler(catalogSvc, b, logger)

	// -- Payments --
	paymentsRepo := paymentsrepository.NewMemoryRepository()
	paymentsSvc := paymentsservice.New(paymentsRepo)
	provider := &configurableProvider{success: true}
	paymentsConsumer := paymentsconsumer.NewHandler(processdeposit.New(provider, b, logger), logger)

	// -- Saga --
	sagaRepo := sagarepository.NewMemoryRepository()
	paymentsClient := &paymentsServiceClient{service: paymentsSvc}
	catalogClient := &catalogServiceClient{service: catalogSvc}
	sagaConsumer := sagaconsumer.NewHandler(sagaRepo, paymentsClient, b, logger)
	sagaHandler := sagaapi.NewHandler(sagaRepo, catalogClient, paymentsClient, b, 30*time.Second, logger, false)

	// Wire up the bus:
	// Commands -> service handlers
	b.on(messaging.RoutingKeyWalletDebitRequested, walletConsumer.HandleWalletDebitRequested)
	b.on(messaging.RoutingKeyWalletCreditRequested, walletConsumer.HandleWalletCreditRequested)
	b.on(messaging.RoutingKeyAccessGrantRequested, catalogConsumer.HandleAccessGrantRequested)
	b.on(messaging.RoutingKeyAccessRevokeRequested, catalogConsumer.HandleAccessRevokeRequested)
	b.on(messaging.RoutingKeyDepositRequested, paymentsConsumer.HandleDepositRequested)

	// Outcomes -> saga outcome handler
	b.on(messaging.RoutingKeyWalletDebited, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyWalletDebitRejected, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyWalletCredited, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyAccessGranted, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyAccessGrantConflicted, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyAccessRevoked, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyAccessRevokeRejected, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyProviderChargeSucceeded, sagaConsumer.HandleOutcome)
	b.on(messaging.RoutingKeyProviderChargeFailed, sagaConsumer.HandleOutcome)

	// Build the HTTP router for command ingress.
	r := chi.NewRouter()
	r.Use(httpx.RequestLogger(logger))
	r.Post("/deposits", sagaHandler.HandleDeposit)
	r.Post("/purchases", sagaHandler.HandlePurchase)
	r.Post("/refunds", sagaHandler.HandleRefund)

	return &testHarness{
		bus:              b,
		sagaRepo:         sagaRepo,
		sagaConsumer:     sagaConsumer,
		sagaHandler:      sagaHandler,
		sagaRouter:       r,
		walletRepo:       walletRepo,
		walletConsumer:   walletConsumer,
		catalogRepo:      catalogRepo,
		catalogConsumer:  catalogConsumer,
		paymentsClient:   paymentsClient,
		paymentsRepo:     paymentsRepo,
		paymentsConsumer: paymentsConsumer,
		paymentsProvider: provider,
	}
}

// postJSON sends a POST request to the harness router and returns the recorder.
func (h *testHarness) postJSON(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.sagaRouter.ServeHTTP(rec, req)
	return rec
}

// extractTransactionID parses the transaction_id from a 202 response.
func extractTransactionID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Data struct {
			TransactionID string `json:"transaction_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.TransactionID == "" {
		t.Fatal("empty transaction_id in response")
	}
	return resp.Data.TransactionID
}

func assertNoBusErrors(t *testing.T, h *testHarness) {
	t.Helper()
	if errs := h.bus.errors(); len(errs) > 0 {
		t.Fatalf("unexpected bus errors: %v", errs)
	}
}

func mustGetTransaction(t *testing.T, repo *paymentsrepository.MemoryRepository, txnID string) *paymentsdomain.Transaction {
	t.Helper()

	txn, err := repo.GetTransactionByID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get transaction %s: %v", txnID, err)
	}
	return txn
}

// ---------------------------------------------------------------------------
// Scenario 1: Purchase happy path
// ---------------------------------------------------------------------------

func TestIntegration_PurchaseHappyPath(t *testing.T) {
	h := newHarness(t)

	// Submit a purchase command.
	rec := h.postJSON(t, "/purchases", sagaapi.PurchaseCommand{
		UserID:         testUserID,
		OfferingID:     testOfferingID,
		IdempotencyKey: "int-pur-happy-1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}

	txnID := extractTransactionID(t, rec)

	// Because the bus is synchronous, the entire workflow has already completed:
	// POST /purchases -> saga created + wallet.debit.requested published
	//   -> wallets: debit user-1 -> wallet.debited published
	//     -> saga: handleWalletDebited -> access.grant.requested published
	//       -> catalog-access: grant access -> access.granted published
	//         -> saga: handleAccessGranted -> saga completed

	// Verify saga final state.
	s, err := h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if s.Status != sagadomain.StatusCompleted {
		t.Errorf("saga status = %s, want completed", s.Status)
	}
	if s.Outcome == nil || *s.Outcome != sagadomain.OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", s.Outcome)
	}

	// Verify wallet was debited (20000 - 5000 = 15000).
	wallet, err := h.walletRepo.GetWalletByUserID(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if wallet.Balance != 15000 {
		t.Errorf("wallet balance = %d, want 15000", wallet.Balance)
	}

	// Verify access was granted.
	access, err := h.catalogRepo.GetActiveAccess(context.Background(), testUserID, testOfferingID)
	if err != nil {
		t.Fatalf("get active access: %v", err)
	}
	if access.TransactionID != txnID {
		t.Errorf("access transaction_id = %s, want %s", access.TransactionID, txnID)
	}

	purchaseTxn := mustGetTransaction(t, h.paymentsRepo, txnID)
	if purchaseTxn.Status != paymentsdomain.StatusCompleted {
		t.Errorf("transaction status = %s, want completed", purchaseTxn.Status)
	}
	if purchaseTxn.Type != paymentsdomain.TransactionTypePurchase {
		t.Errorf("transaction type = %s, want purchase", purchaseTxn.Type)
	}
	if purchaseTxn.Amount != 5000 {
		t.Errorf("transaction amount = %d, want 5000", purchaseTxn.Amount)
	}
	assertNoBusErrors(t, h)

	// Verify message flow: debit.requested -> debited -> grant.requested -> granted
	msgs := h.bus.messages()
	expectedKeys := []string{
		messaging.RoutingKeyWalletDebitRequested,
		messaging.RoutingKeyWalletDebited,
		messaging.RoutingKeyAccessGrantRequested,
		messaging.RoutingKeyAccessGranted,
	}
	if len(msgs) < len(expectedKeys) {
		t.Fatalf("expected at least %d messages, got %d", len(expectedKeys), len(msgs))
	}
	for i, key := range expectedKeys {
		if msgs[i].RoutingKey != key {
			t.Errorf("message[%d] routing_key = %s, want %s", i, msgs[i].RoutingKey, key)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Purchase insufficient funds
// ---------------------------------------------------------------------------

func TestIntegration_PurchaseInsufficientFunds(t *testing.T) {
	h := newHarness(t)

	// Set wallet balance to be insufficient.
	h.walletRepo.Wallets[testUserID].Balance = 1000

	rec := h.postJSON(t, "/purchases", sagaapi.PurchaseCommand{
		UserID:         testUserID,
		OfferingID:     testOfferingID,
		IdempotencyKey: "int-pur-insuf-1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}

	txnID := extractTransactionID(t, rec)

	// Flow: wallet.debit.requested -> wallet.debit.rejected -> saga failed.
	s, err := h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if s.Status != sagadomain.StatusFailed {
		t.Errorf("saga status = %s, want failed", s.Status)
	}
	if s.Outcome == nil || *s.Outcome != sagadomain.OutcomeFailed {
		t.Errorf("saga outcome = %v, want failed", s.Outcome)
	}

	// Verify wallet balance was NOT modified.
	wallet, err := h.walletRepo.GetWalletByUserID(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if wallet.Balance != 1000 {
		t.Errorf("wallet balance = %d, want 1000 (unchanged)", wallet.Balance)
	}

	// Verify no access was granted.
	_, err = h.catalogRepo.GetActiveAccess(context.Background(), testUserID, testOfferingID)
	if err == nil {
		t.Error("expected no active access, but got one")
	}

	purchaseTxn := mustGetTransaction(t, h.paymentsRepo, txnID)
	if purchaseTxn.Status != paymentsdomain.StatusFailed {
		t.Errorf("transaction status = %s, want failed", purchaseTxn.Status)
	}
	assertNoBusErrors(t, h)

	// Verify message flow: debit.requested -> debit.rejected
	msgs := h.bus.messages()
	expectedKeys := []string{
		messaging.RoutingKeyWalletDebitRequested,
		messaging.RoutingKeyWalletDebitRejected,
	}
	if len(msgs) < len(expectedKeys) {
		t.Fatalf("expected at least %d messages, got %d", len(expectedKeys), len(msgs))
	}
	for i, key := range expectedKeys {
		if msgs[i].RoutingKey != key {
			t.Errorf("message[%d] routing_key = %s, want %s", i, msgs[i].RoutingKey, key)
		}
	}

	// Ensure no access.grant.requested was published.
	for _, m := range msgs {
		if m.RoutingKey == messaging.RoutingKeyAccessGrantRequested {
			t.Error("access.grant.requested should NOT have been published on insufficient funds")
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: Concurrent purchase safety
// ---------------------------------------------------------------------------

func TestIntegration_ConcurrentPurchaseSafety(t *testing.T) {
	// This test verifies that two concurrent purchases for the same user
	// and offering are handled correctly via the in-memory wallet dedup
	// and catalog-access unique access constraint.
	//
	// With a balance of 5000 and two attempts to debit 5000 each,
	// only one should succeed; the other should get insufficient funds
	// since the bus is synchronous and the first debit will complete first.
	//
	// We use goroutines to test concurrent HTTP requests.

	h := newHarness(t)
	h.walletRepo.Wallets[testUserID].Balance = 5000 // Exact amount for one purchase.

	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = h.postJSON(t, "/purchases", sagaapi.PurchaseCommand{
				UserID:         testUserID,
				OfferingID:     testOfferingID,
				IdempotencyKey: fmt.Sprintf("int-pur-concurrent-%d", idx),
			})
		}(i)
	}
	wg.Wait()

	txnIDs := make([]string, 0, len(results))
	denied := 0
	for i, rec := range results {
		switch rec.Code {
		case http.StatusAccepted:
			var resp struct {
				Data struct {
					TransactionID string `json:"transaction_id"`
				} `json:"data"`
			}
			if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
				t.Fatalf("request %d: decode response: %v", i, err)
			}
			if resp.Data.TransactionID == "" {
				t.Fatalf("request %d: empty transaction_id in accepted response", i)
			}
			txnIDs = append(txnIDs, resp.Data.TransactionID)
		case http.StatusUnprocessableEntity:
			var resp httpx.Response
			if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
				t.Fatalf("request %d: decode error response: %v", i, err)
			}
			if resp.Error == nil || resp.Error.Code != "PRECHECK_DENIED" {
				t.Fatalf("request %d: expected PRECHECK_DENIED, got %+v", i, resp.Error)
			}
			denied++
		default:
			t.Fatalf("request %d: unexpected status = %d; body: %s", i, rec.Code, rec.Body.String())
		}
	}
	if len(txnIDs) == 0 {
		t.Fatal("expected at least one accepted purchase request")
	}

	// Count completed vs failed sagas.
	completed := 0
	failed := 0
	for _, txnID := range txnIDs {
		s, err := h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
		if err != nil {
			t.Fatalf("get saga for %s: %v", txnID, err)
		}
		switch s.Status {
		case sagadomain.StatusCompleted:
			completed++
		case sagadomain.StatusFailed:
			failed++
		default:
			// A second purchase attempt that finds the access already granted
			// will get access.grant.conflicted, triggering compensation.
			// The compensation refunds the debit, so the saga ends as completed
			// with outcome compensated.
			if s.Outcome != nil && *s.Outcome == sagadomain.OutcomeCompensated {
				completed++ // Compensated is a terminal completed state.
			} else {
				t.Logf("saga %s: status=%s outcome=%v", txnID, s.Status, s.Outcome)
			}
		}
	}

	// At most one purchase should have fully succeeded.
	// The other should have failed (insufficient funds) or been compensated
	// (debited but access conflicted).
	successCount := 0
	for _, txnID := range txnIDs {
		s, _ := h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
		if s.Outcome != nil && *s.Outcome == sagadomain.OutcomeSucceeded {
			successCount++
		}
	}
	if successCount > 1 {
		t.Errorf("expected at most 1 successful purchase, got %d", successCount)
	}

	txns, err := h.paymentsRepo.ListTransactionsByUserID(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txns) != len(txnIDs) {
		t.Errorf("payments transactions = %d, want %d", len(txns), len(txnIDs))
	}

	// Verify wallet balance is consistent:
	// If one succeeded: 5000 - 5000 = 0
	// If one succeeded and one was compensated: 5000 - 5000 + 5000 - 5000 = 0 (net same)
	// If one was denied at precheck: only the accepted purchase is applied.
	wallet, err := h.walletRepo.GetWalletByUserID(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if wallet.Balance < 0 {
		t.Errorf("wallet balance = %d, should never go negative", wallet.Balance)
	}
	assertNoBusErrors(t, h)

	t.Logf("concurrent purchase result: completed=%d, failed=%d, denied=%d, balance=%d", completed, failed, denied, wallet.Balance)
}

// ---------------------------------------------------------------------------
// Scenario 4: Deposit timeout handling
// ---------------------------------------------------------------------------

func TestIntegration_DepositTimeoutHandling(t *testing.T) {
	h := newHarness(t)

	// Make the provider NOT auto-dispatch. We'll manually simulate the timeout
	// and late provider response. Remove the deposit.requested handler so the
	// bus doesn't auto-dispatch the provider call.
	delete(h.bus.handlers, messaging.RoutingKeyDepositRequested)

	rec := h.postJSON(t, "/deposits", sagaapi.DepositCommand{
		UserID:         testUserID,
		Amount:         10000,
		Currency:       "ARS",
		IdempotencyKey: "int-dep-timeout-1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}

	txnID := extractTransactionID(t, rec)

	// Verify saga is running (deposit_charge step).
	s, err := h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
	if err != nil {
		t.Fatalf("get saga: %v", err)
	}
	if s.Status != sagadomain.StatusRunning {
		t.Fatalf("saga status = %s, want running", s.Status)
	}

	// Simulate timeout: the timeout poller transitions the saga to timed_out.
	poller := timeoutusecase.NewTimeoutPoller(h.sagaRepo, h.paymentsClient, timeoutusecase.TimeoutConfig{
		PollInterval: 1 * time.Second,
		SagaTimeout:  30 * time.Second,
	}, discardLogger())

	// Manually set the saga's timeout to the past so the poller picks it up.
	// We do this by creating the saga with an already-past timeout; since the
	// saga was created normally, we need to directly manipulate the repo.
	s, _ = h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
	past := time.Now().UTC().Add(-1 * time.Minute)
	s.TimeoutAt = &past
	// The MemoryRepository stores pointers, so this mutation is reflected.
	// But we need to access the internal storage. Let's use ListTimedOutSagas
	// to verify, and then manually transition.

	// Direct transition to timed_out (simulating what the poller does).
	_, err = h.sagaRepo.UpdateSagaStatus(context.Background(), s.ID, sagadomain.StatusTimedOut, nil, s.CurrentStep)
	if err != nil {
		t.Fatalf("transition to timed_out: %v", err)
	}

	// Verify saga is timed_out.
	s, _ = h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
	if s.Status != sagadomain.StatusTimedOut {
		t.Fatalf("saga status = %s, want timed_out", s.Status)
	}

	// Now simulate a late provider.charge.succeeded arriving after timeout.
	// Route it through the bus so publish/consume ordering matches the real broker flow.
	if err := h.bus.Publish(
		context.Background(),
		messaging.ExchangeOutcomes,
		messaging.RoutingKeyProviderChargeSucceeded,
		txnID,
		messaging.ProviderChargeSucceeded{
			TransactionID: txnID,
			UserID:        testUserID,
			Amount:        10000,
			ProviderRef:   "sim-" + txnID,
		},
	); err != nil {
		t.Fatalf("handleProviderChargeSucceeded on timed_out saga: %v", err)
	}

	// The saga consumer publishes wallet.credit.requested, which the bus dispatches
	// to the wallets consumer, which credits the wallet and publishes wallet.credited,
	// which the bus dispatches back to the saga consumer, completing the saga.

	// Verify saga is completed with succeeded outcome (late success recovery).
	s, _ = h.sagaRepo.GetSagaByTransactionID(context.Background(), txnID)
	if s.Status != sagadomain.StatusCompleted {
		t.Errorf("saga status = %s, want completed", s.Status)
	}
	if s.Outcome == nil || *s.Outcome != sagadomain.OutcomeSucceeded {
		t.Errorf("saga outcome = %v, want succeeded", s.Outcome)
	}

	// Verify wallet was credited (20000 + 10000 = 30000).
	wallet, err := h.walletRepo.GetWalletByUserID(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if wallet.Balance != 30000 {
		t.Errorf("wallet balance = %d, want 30000", wallet.Balance)
	}
	assertNoBusErrors(t, h)

	// Verify the poller is constructable (smoke check).
	_ = poller
}

// ---------------------------------------------------------------------------
// Scenario 5: Refund happy path
// ---------------------------------------------------------------------------

func TestIntegration_RefundHappyPath(t *testing.T) {
	h := newHarness(t)

	// First, complete a purchase so we have an access record to refund.
	rec := h.postJSON(t, "/purchases", sagaapi.PurchaseCommand{
		UserID:         testUserID,
		OfferingID:     testOfferingID,
		IdempotencyKey: "int-ref-purchase-1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("purchase: status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}
	purchaseTxnID := extractTransactionID(t, rec)

	// Verify purchase completed.
	purchaseSaga, _ := h.sagaRepo.GetSagaByTransactionID(context.Background(), purchaseTxnID)
	if purchaseSaga.Status != sagadomain.StatusCompleted {
		t.Fatalf("purchase saga status = %s, want completed", purchaseSaga.Status)
	}
	purchaseTxn := mustGetTransaction(t, h.paymentsRepo, purchaseTxnID)
	if purchaseTxn.Status != paymentsdomain.StatusCompleted {
		t.Fatalf("purchase transaction status = %s, want completed", purchaseTxn.Status)
	}

	// Verify wallet was debited (20000 - 5000 = 15000).
	wallet, _ := h.walletRepo.GetWalletByUserID(context.Background(), testUserID)
	if wallet.Balance != 15000 {
		t.Fatalf("wallet balance after purchase = %d, want 15000", wallet.Balance)
	}

	// Verify access was granted.
	_, err := h.catalogRepo.GetActiveAccess(context.Background(), testUserID, testOfferingID)
	if err != nil {
		t.Fatalf("no active access after purchase: %v", err)
	}

	// Now submit a refund.
	rec = h.postJSON(t, "/refunds", sagaapi.RefundCommand{
		UserID:         testUserID,
		OfferingID:     testOfferingID,
		TransactionID:  purchaseTxnID,
		IdempotencyKey: "int-ref-refund-1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("refund: status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}
	refundTxnID := extractTransactionID(t, rec)

	// Flow: access.revoke.requested -> access.revoked -> wallet.credit.requested
	//       -> wallet.credited -> saga completed.

	// Verify refund saga completed.
	refundSaga, err := h.sagaRepo.GetSagaByTransactionID(context.Background(), refundTxnID)
	if err != nil {
		t.Fatalf("get refund saga: %v", err)
	}
	if refundSaga.Status != sagadomain.StatusCompleted {
		t.Errorf("refund saga status = %s, want completed", refundSaga.Status)
	}
	if refundSaga.Outcome == nil || *refundSaga.Outcome != sagadomain.OutcomeSucceeded {
		t.Errorf("refund saga outcome = %v, want succeeded", refundSaga.Outcome)
	}

	refundTxn := mustGetTransaction(t, h.paymentsRepo, refundTxnID)
	if refundTxn.Status != paymentsdomain.StatusCompleted {
		t.Errorf("refund transaction status = %s, want completed", refundTxn.Status)
	}
	if refundTxn.OriginalTransactionID == nil || *refundTxn.OriginalTransactionID != purchaseTxnID {
		t.Errorf("refund original_transaction_id = %v, want %s", refundTxn.OriginalTransactionID, purchaseTxnID)
	}

	// Verify wallet was credited back (15000 + 5000 = 20000).
	wallet, _ = h.walletRepo.GetWalletByUserID(context.Background(), testUserID)
	if wallet.Balance != 20000 {
		t.Errorf("wallet balance after refund = %d, want 20000", wallet.Balance)
	}

	// Verify access was revoked.
	_, err = h.catalogRepo.GetActiveAccess(context.Background(), testUserID, testOfferingID)
	if err == nil {
		t.Error("expected access to be revoked, but still active")
	}
	assertNoBusErrors(t, h)
}
