package saga

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

// DefaultSagaTimeout is the default duration before a saga times out.
const DefaultSagaTimeout = 30 * time.Second

// Publisher defines the messaging interface used by the handler to dispatch
// async commands after saga creation.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// Handler provides HTTP handlers for the saga-orchestrator command ingress.
type Handler struct {
	repo           Repository
	catalogClient  CatalogClient
	paymentsClient PaymentsClient
	publisher      Publisher
	sagaTimeout    time.Duration
	logger         *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(
	repo Repository,
	catalogClient CatalogClient,
	paymentsClient PaymentsClient,
	publisher Publisher,
	sagaTimeout time.Duration,
	logger *slog.Logger,
) *Handler {
	if sagaTimeout == 0 {
		sagaTimeout = DefaultSagaTimeout
	}
	return &Handler{
		repo:           repo,
		catalogClient:  catalogClient,
		paymentsClient: paymentsClient,
		publisher:      publisher,
		sagaTimeout:    sagaTimeout,
		logger:         logger,
	}
}

// HandleDeposit handles POST /deposits.
// Accepts a deposit command, registers the transaction, creates a saga, and returns 202.
func (h *Handler) HandleDeposit(w http.ResponseWriter, r *http.Request) {
	var cmd DepositCommand
	if err := httpx.Decode(r, &cmd); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if cmd.UserID == "" || cmd.Amount <= 0 || cmd.IdempotencyKey == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id, a positive amount, and idempotency_key are required")
		return
	}
	if cmd.Currency == "" {
		cmd.Currency = "ARS"
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", cmd.UserID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	// Check idempotency.
	if cached, err := h.checkIdempotency(r, w, cmd.IdempotencyKey); cached || err != nil {
		return
	}

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	// Step 1: Register transaction in payments (sync).
	_, err := h.paymentsClient.RegisterTransaction(r.Context(), RegisterTransactionRequest{
		ID:       transactionID,
		UserID:   cmd.UserID,
		Type:     "deposit",
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
	})
	if err != nil {
		logger.Error("failed to register transaction in payments", "error", err)
		httpx.Error(w, http.StatusBadGateway, "payment service unavailable")
		return
	}

	// Step 2: Create saga.
	payload, _ := json.Marshal(DepositPayload{
		UserID:   cmd.UserID,
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
	})

	timeoutAt := time.Now().UTC().Add(h.sagaTimeout)
	sagaInstance, err := h.repo.CreateSaga(r.Context(), &SagaInstance{
		TransactionID: transactionID,
		Type:          SagaTypeDeposit,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &timeoutAt,
	})
	if err != nil {
		logger.Error("failed to create saga", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	logger.Info("deposit saga created", "saga_id", sagaInstance.ID)

	// Save idempotency key.
	h.saveIdempotencyKey(r.Context(), cmd.IdempotencyKey, transactionID, http.StatusAccepted)

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: transactionID,
		Status:        "pending",
	})
}

// HandlePurchase handles POST /purchases.
// Validates via catalog-access precheck, registers the transaction, creates a saga, and returns 202.
func (h *Handler) HandlePurchase(w http.ResponseWriter, r *http.Request) {
	var cmd PurchaseCommand
	if err := httpx.Decode(r, &cmd); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if cmd.UserID == "" || cmd.OfferingID == "" || cmd.Amount <= 0 || cmd.IdempotencyKey == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id, offering_id, a positive amount, and idempotency_key are required")
		return
	}
	if cmd.Currency == "" {
		cmd.Currency = "ARS"
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	// Check idempotency.
	if cached, err := h.checkIdempotency(r, w, cmd.IdempotencyKey); cached || err != nil {
		return
	}

	// Step 1: Purchase precheck (sync, fail-fast).
	precheck, err := h.catalogClient.PurchasePrecheck(r.Context(), cmd.UserID, cmd.OfferingID)
	if err != nil {
		logger.Error("purchase precheck failed", "error", err)
		httpx.Error(w, http.StatusBadGateway, "catalog service unavailable")
		return
	}
	if !precheck.Allowed {
		logger.Info("purchase precheck denied", "reason", precheck.Reason)
		httpx.ErrorWithCode(w, http.StatusUnprocessableEntity, precheck.Reason, "PRECHECK_DENIED")
		return
	}

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	// Step 2: Register transaction in payments (sync).
	_, err = h.paymentsClient.RegisterTransaction(r.Context(), RegisterTransactionRequest{
		ID:         transactionID,
		UserID:     cmd.UserID,
		Type:       "purchase",
		Amount:     cmd.Amount,
		Currency:   cmd.Currency,
		OfferingID: &cmd.OfferingID,
	})
	if err != nil {
		logger.Error("failed to register transaction in payments", "error", err)
		httpx.Error(w, http.StatusBadGateway, "payment service unavailable")
		return
	}

	// Step 3: Create saga.
	payload, _ := json.Marshal(PurchasePayload{
		UserID:     cmd.UserID,
		OfferingID: cmd.OfferingID,
		Amount:     cmd.Amount,
		Currency:   cmd.Currency,
	})

	timeoutAt := time.Now().UTC().Add(h.sagaTimeout)
	sagaInstance, err := h.repo.CreateSaga(r.Context(), &SagaInstance{
		TransactionID: transactionID,
		Type:          SagaTypePurchase,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &timeoutAt,
	})
	if err != nil {
		logger.Error("failed to create saga", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	logger.Info("purchase saga created", "saga_id", sagaInstance.ID)

	// Save idempotency key.
	h.saveIdempotencyKey(r.Context(), cmd.IdempotencyKey, transactionID, http.StatusAccepted)

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: transactionID,
		Status:        "pending",
	})
}

// HandleRefund handles POST /refunds.
// Validates via catalog-access refund precheck, registers the transaction, creates a saga, and returns 202.
func (h *Handler) HandleRefund(w http.ResponseWriter, r *http.Request) {
	var cmd RefundCommand
	if err := httpx.Decode(r, &cmd); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if cmd.UserID == "" || cmd.OfferingID == "" || cmd.TransactionID == "" || cmd.Amount <= 0 || cmd.IdempotencyKey == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id, offering_id, transaction_id, a positive amount, and idempotency_key are required")
		return
	}
	if cmd.Currency == "" {
		cmd.Currency = "ARS"
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
		slog.String("original_transaction_id", cmd.TransactionID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	// Check idempotency.
	if cached, err := h.checkIdempotency(r, w, cmd.IdempotencyKey); cached || err != nil {
		return
	}

	// Step 1: Refund precheck (sync, fail-fast).
	precheck, err := h.catalogClient.RefundPrecheck(r.Context(), cmd.UserID, cmd.OfferingID, cmd.TransactionID)
	if err != nil {
		logger.Error("refund precheck failed", "error", err)
		httpx.Error(w, http.StatusBadGateway, "catalog service unavailable")
		return
	}
	if !precheck.Allowed {
		logger.Info("refund precheck denied", "reason", precheck.Reason)
		httpx.ErrorWithCode(w, http.StatusUnprocessableEntity, precheck.Reason, "PRECHECK_DENIED")
		return
	}

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	// Step 2: Register transaction in payments (sync).
	_, err = h.paymentsClient.RegisterTransaction(r.Context(), RegisterTransactionRequest{
		ID:         transactionID,
		UserID:     cmd.UserID,
		Type:       "refund",
		Amount:     cmd.Amount,
		Currency:   cmd.Currency,
		OfferingID: &cmd.OfferingID,
	})
	if err != nil {
		logger.Error("failed to register transaction in payments", "error", err)
		httpx.Error(w, http.StatusBadGateway, "payment service unavailable")
		return
	}

	// Step 3: Create saga.
	payload, _ := json.Marshal(RefundPayload{
		UserID:              cmd.UserID,
		OfferingID:          cmd.OfferingID,
		OriginalTransaction: cmd.TransactionID,
		Amount:              cmd.Amount,
		Currency:            cmd.Currency,
	})

	timeoutAt := time.Now().UTC().Add(h.sagaTimeout)
	sagaInstance, err := h.repo.CreateSaga(r.Context(), &SagaInstance{
		TransactionID: transactionID,
		Type:          SagaTypeRefund,
		Status:        StatusCreated,
		Payload:       payload,
		TimeoutAt:     &timeoutAt,
	})
	if err != nil {
		logger.Error("failed to create saga", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	logger.Info("refund saga created", "saga_id", sagaInstance.ID)

	// Save idempotency key.
	h.saveIdempotencyKey(r.Context(), cmd.IdempotencyKey, transactionID, http.StatusAccepted)

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: transactionID,
		Status:        "pending",
	})
}

// checkIdempotency checks if the idempotency key has been seen before.
// Returns (true, nil) if a cached response was sent. Returns (false, nil) if no
// cached entry exists and the caller should proceed. Returns (false, error) if
// there was an internal error (already written to the response writer).
func (h *Handler) checkIdempotency(r *http.Request, w http.ResponseWriter, key string) (bool, error) {
	cached, err := h.repo.GetIdempotencyKey(r.Context(), key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		h.logger.Error("failed to check idempotency key", "error", err, "key", key)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return false, err
	}

	// Replay the cached response.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(cached.ResponseStatus)
	if cached.ResponseBody != nil {
		w.Write(cached.ResponseBody)
	}
	return true, nil
}

// saveIdempotencyKey stores the accepted response so duplicates get the same result.
func (h *Handler) saveIdempotencyKey(ctx context.Context, key, transactionID string, status int) {
	respBody, _ := json.Marshal(httpx.Response{
		Success: true,
		Data: CommandAcceptedResponse{
			TransactionID: transactionID,
			Status:        "pending",
		},
	})

	ik := &IdempotencyKey{
		Key:            key,
		TransactionID:  transactionID,
		ResponseStatus: status,
		ResponseBody:   respBody,
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}
	if err := h.repo.SaveIdempotencyKey(ctx, ik); err != nil && !errors.Is(err, ErrIdempotencyKeyExists) {
		h.logger.Error("failed to save idempotency key", "error", err, "key", key)
	}
}
