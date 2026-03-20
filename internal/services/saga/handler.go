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
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
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

	if fields := validateDepositCommand(&cmd); len(fields) > 0 {
		httpx.ValidationError(w, fields)
		return
	}
	if cmd.Currency == "" {
		cmd.Currency = "ARS"
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", cmd.UserID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	// Compute request fingerprint from the validated, normalized command.
	scope := string(SagaTypeDeposit)
	requestHash := RequestFingerprint(DepositPayload{
		UserID:   cmd.UserID,
		Amount:   cmd.Amount,
		Currency: cmd.Currency,
	})

	// Check idempotency.
	if cached, err := h.checkIdempotency(r, w, scope, cmd.IdempotencyKey, requestHash); cached || err != nil {
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

	// Step 3: Transition saga to running and publish payments.deposit.requested.
	step := "deposit_charge"
	if _, err := h.repo.UpdateSagaStatus(r.Context(), sagaInstance.ID, StatusRunning, nil, &step); err != nil {
		logger.Error("failed to transition saga to running", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := h.publisher.Publish(r.Context(),
		messaging.ExchangeCommands,
		messaging.RoutingKeyDepositRequested,
		transactionID,
		messaging.DepositRequested{
			TransactionID: transactionID,
			UserID:        cmd.UserID,
			Amount:        cmd.Amount,
			Currency:      cmd.Currency,
		},
	); err != nil {
		logger.Error("failed to publish payments.deposit.requested", "error", err)
		// Saga is already running but command was not sent. The timeout poller
		// will eventually handle this. We still return 202 since the saga exists.
	}

	// Save idempotency key.
	h.saveIdempotencyKey(r.Context(), scope, cmd.IdempotencyKey, requestHash, transactionID, http.StatusAccepted)

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

	if fields := validatePurchaseCommand(&cmd); len(fields) > 0 {
		httpx.ValidationError(w, fields)
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	// Compute request fingerprint from the validated, normalized command.
	scope := string(SagaTypePurchase)
	requestHash := RequestFingerprint(PurchasePayload{
		UserID:     cmd.UserID,
		OfferingID: cmd.OfferingID,
	})

	// Check idempotency.
	if cached, err := h.checkIdempotency(r, w, scope, cmd.IdempotencyKey, requestHash); cached || err != nil {
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
	if precheck.Price <= 0 || precheck.Currency == "" {
		logger.Error("purchase precheck returned invalid pricing", "price", precheck.Price, "currency", precheck.Currency)
		httpx.Error(w, http.StatusBadGateway, "catalog service unavailable")
		return
	}

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	// Step 2: Register transaction in payments (sync).
	_, err = h.paymentsClient.RegisterTransaction(r.Context(), RegisterTransactionRequest{
		ID:         transactionID,
		UserID:     cmd.UserID,
		Type:       "purchase",
		Amount:     precheck.Price,
		Currency:   precheck.Currency,
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
		Amount:     precheck.Price,
		Currency:   precheck.Currency,
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

	// Step 4: Transition saga to running and publish wallet.debit.requested.
	step := "purchase_debit"
	if _, err := h.repo.UpdateSagaStatus(r.Context(), sagaInstance.ID, StatusRunning, nil, &step); err != nil {
		logger.Error("failed to transition saga to running", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := h.publisher.Publish(r.Context(),
		messaging.ExchangeCommands,
		messaging.RoutingKeyWalletDebitRequested,
		transactionID,
		messaging.WalletDebitRequested{
			TransactionID: transactionID,
			UserID:        cmd.UserID,
			Amount:        precheck.Price,
			Currency:      precheck.Currency,
			SourceStep:    step,
		},
	); err != nil {
		logger.Error("failed to publish wallet.debit.requested", "error", err)
		// Saga is already running but command was not sent. The timeout poller
		// will eventually handle this. We still return 202 since the saga exists.
	}

	// Save idempotency key.
	h.saveIdempotencyKey(r.Context(), scope, cmd.IdempotencyKey, requestHash, transactionID, http.StatusAccepted)

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

	if fields := validateRefundCommand(&cmd); len(fields) > 0 {
		httpx.ValidationError(w, fields)
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
		slog.String("original_transaction_id", cmd.TransactionID),
		slog.String("idempotency_key", cmd.IdempotencyKey),
	)

	// Compute request fingerprint from the validated, normalized command.
	scope := string(SagaTypeRefund)
	requestHash := RequestFingerprint(RefundPayload{
		UserID:              cmd.UserID,
		OfferingID:          cmd.OfferingID,
		OriginalTransaction: cmd.TransactionID,
	})

	// Check idempotency.
	if cached, err := h.checkIdempotency(r, w, scope, cmd.IdempotencyKey, requestHash); cached || err != nil {
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

	originalTxn, err := h.paymentsClient.GetTransaction(r.Context(), cmd.TransactionID)
	if err != nil {
		logger.Error("failed to fetch original transaction", "error", err)
		httpx.Error(w, http.StatusBadGateway, "payment service unavailable")
		return
	}
	if originalTxn.Type != "purchase" {
		httpx.ErrorWithCode(w, http.StatusUnprocessableEntity, "original transaction must be a purchase", "INVALID_ORIGINAL_TRANSACTION")
		return
	}
	if originalTxn.UserID != cmd.UserID {
		httpx.ErrorWithCode(w, http.StatusUnprocessableEntity, "original transaction does not belong to user", "INVALID_ORIGINAL_TRANSACTION")
		return
	}
	if originalTxn.OfferingID == nil || *originalTxn.OfferingID != cmd.OfferingID {
		httpx.ErrorWithCode(w, http.StatusUnprocessableEntity, "original transaction does not match offering", "INVALID_ORIGINAL_TRANSACTION")
		return
	}
	if originalTxn.Amount <= 0 || originalTxn.Currency == "" {
		httpx.ErrorWithCode(w, http.StatusUnprocessableEntity, "original transaction has invalid amount", "INVALID_ORIGINAL_TRANSACTION")
		return
	}

	transactionID := uuid.New().String()
	logger = logger.With(slog.String(logging.KeyTransactionID, transactionID))

	// Step 2: Register transaction in payments (sync).
	_, err = h.paymentsClient.RegisterTransaction(r.Context(), RegisterTransactionRequest{
		ID:         transactionID,
		UserID:     cmd.UserID,
		Type:       "refund",
		Amount:     originalTxn.Amount,
		Currency:   originalTxn.Currency,
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
		Amount:              originalTxn.Amount,
		Currency:            originalTxn.Currency,
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

	// Step 4: Transition saga to running and publish access.revoke.requested.
	step := "refund_revoke_access"
	if _, err := h.repo.UpdateSagaStatus(r.Context(), sagaInstance.ID, StatusRunning, nil, &step); err != nil {
		logger.Error("failed to transition saga to running", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := h.publisher.Publish(r.Context(),
		messaging.ExchangeCommands,
		messaging.RoutingKeyAccessRevokeRequested,
		transactionID, // refund transaction ID as correlation
		messaging.AccessRevokeRequested{
			TransactionID: cmd.TransactionID, // original purchase transaction ID for DB lookup
			UserID:        cmd.UserID,
			OfferingID:    cmd.OfferingID,
		},
	); err != nil {
		logger.Error("failed to publish access.revoke.requested", "error", err)
		// Saga is already running but command was not sent. The timeout poller
		// will eventually handle this. We still return 202 since the saga exists.
	}

	// Save idempotency key.
	h.saveIdempotencyKey(r.Context(), scope, cmd.IdempotencyKey, requestHash, transactionID, http.StatusAccepted)

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: transactionID,
		Status:        "pending",
	})
}

// checkIdempotency checks if the idempotency key has been seen before.
// Returns (true, nil) if a cached response was sent or a conflict was returned.
// Returns (false, nil) if no cached entry exists and the caller should proceed.
// Returns (false, error) if there was an internal error (already written to w).
func (h *Handler) checkIdempotency(r *http.Request, w http.ResponseWriter, scope, key, requestHash string) (bool, error) {
	cached, err := h.repo.GetIdempotencyKey(r.Context(), scope, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		h.logger.Error("failed to check idempotency key", "error", err, "key", key, "scope", scope)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return false, err
	}

	// Same scope+key but different payload hash -> conflict.
	if cached.RequestHash != requestHash {
		httpx.ErrorWithCode(w, http.StatusConflict,
			"idempotency key already used with a different request payload",
			"IDEMPOTENCY_MISMATCH")
		return true, nil
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
func (h *Handler) saveIdempotencyKey(ctx context.Context, scope, key, requestHash, transactionID string, status int) {
	respBody, _ := json.Marshal(httpx.Response{
		Success: true,
		Data: CommandAcceptedResponse{
			TransactionID: transactionID,
			Status:        "pending",
		},
	})

	ik := &IdempotencyKey{
		Key:            key,
		Scope:          scope,
		RequestHash:    requestHash,
		TransactionID:  transactionID,
		ResponseStatus: status,
		ResponseBody:   respBody,
		ExpiresAt:      time.Now().UTC().Add(24 * time.Hour),
	}
	if err := h.repo.SaveIdempotencyKey(ctx, ik); err != nil && !errors.Is(err, ErrIdempotencyKeyExists) {
		h.logger.Error("failed to save idempotency key", "error", err, "key", key, "scope", scope)
	}
}

// ---- Validation helpers ----

// validateDepositCommand returns field-level validation errors for a deposit command.
func validateDepositCommand(cmd *DepositCommand) []httpx.FieldError {
	var fields []httpx.FieldError
	if cmd.UserID == "" {
		fields = append(fields, httpx.FieldError{Field: "user_id", Code: "required"})
	}
	if cmd.Amount <= 0 {
		fields = append(fields, httpx.FieldError{Field: "amount", Code: "must_be_positive"})
	}
	if cmd.IdempotencyKey == "" {
		fields = append(fields, httpx.FieldError{Field: "idempotency_key", Code: "required"})
	}
	return fields
}

// validatePurchaseCommand returns field-level validation errors for a purchase command.
func validatePurchaseCommand(cmd *PurchaseCommand) []httpx.FieldError {
	var fields []httpx.FieldError
	if cmd.UserID == "" {
		fields = append(fields, httpx.FieldError{Field: "user_id", Code: "required"})
	}
	if cmd.OfferingID == "" {
		fields = append(fields, httpx.FieldError{Field: "offering_id", Code: "required"})
	}
	if cmd.IdempotencyKey == "" {
		fields = append(fields, httpx.FieldError{Field: "idempotency_key", Code: "required"})
	}
	return fields
}

// validateRefundCommand returns field-level validation errors for a refund command.
func validateRefundCommand(cmd *RefundCommand) []httpx.FieldError {
	var fields []httpx.FieldError
	if cmd.UserID == "" {
		fields = append(fields, httpx.FieldError{Field: "user_id", Code: "required"})
	}
	if cmd.OfferingID == "" {
		fields = append(fields, httpx.FieldError{Field: "offering_id", Code: "required"})
	}
	if cmd.TransactionID == "" {
		fields = append(fields, httpx.FieldError{Field: "transaction_id", Code: "required"})
	}
	if cmd.IdempotencyKey == "" {
		fields = append(fields, httpx.FieldError{Field: "idempotency_key", Code: "required"})
	}
	return fields
}
