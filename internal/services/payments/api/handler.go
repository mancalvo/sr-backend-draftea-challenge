package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/service"
)

// Handler provides HTTP handlers for the payments service.
type Handler struct {
	service *service.Service
	logger  *slog.Logger
}

const (
	errCodeInvalidTransactionRequest  = "INVALID_TRANSACTION_REQUEST"
	errCodeInvalidTransactionType     = "INVALID_TRANSACTION_TYPE"
	errCodeMissingOriginalTransaction = "MISSING_ORIGINAL_TRANSACTION_ID"
	errCodeMissingTransactionID       = "MISSING_TRANSACTION_ID"
	errCodeInvalidStatus              = "INVALID_STATUS"
	errCodeMissingUserID              = "MISSING_USER_ID"
)

// NewHandler creates a new Handler.
func NewHandler(service *service.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// CreateTransaction handles POST /internal/transactions.
// Registers a new transaction in pending status. The transaction_id is provided
// by the caller (saga-orchestrator generates it via payments service).
func (h *Handler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	var req CreateTransactionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}

	if req.ID == "" || req.UserID == "" || req.Amount <= 0 {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "id, user_id, and a positive amount are required", errCodeInvalidTransactionRequest)
		return
	}
	if !domain.ValidTransactionType(req.Type) {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "invalid transaction type", errCodeInvalidTransactionType)
		return
	}
	if req.Type == domain.TransactionTypeRefund && (req.OriginalTransactionID == nil || *req.OriginalTransactionID == "") {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "original_transaction_id is required for refunds", errCodeMissingOriginalTransaction)
		return
	}
	if req.Currency == "" {
		req.Currency = "ARS"
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String(logging.KeyTransactionID, req.ID),
		slog.String("user_id", req.UserID),
		slog.String("type", string(req.Type)),
	)

	txn := &domain.Transaction{
		ID:                    req.ID,
		UserID:                req.UserID,
		Type:                  req.Type,
		Status:                domain.StatusPending,
		Amount:                req.Amount,
		Currency:              req.Currency,
		OfferingID:            req.OfferingID,
		OriginalTransactionID: req.OriginalTransactionID,
	}

	created, err := h.service.RegisterTransaction(r.Context(), txn)
	if errors.Is(err, repository.ErrDuplicateID) {
		httpx.ErrorWithCode(w, http.StatusConflict, "transaction already exists", "DUPLICATE_TRANSACTION")
		return
	}
	if err != nil {
		logger.Error("failed to create transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	logger.Info("transaction created")
	httpx.JSON(w, http.StatusCreated, created)
}

// UpdateTransactionStatus handles PATCH /internal/transactions/{transaction_id}/status.
// Transitions a transaction to a new status, enforcing the state machine.
func (h *Handler) UpdateTransactionStatus(w http.ResponseWriter, r *http.Request) {
	txnID := chi.URLParam(r, "transaction_id")
	if txnID == "" {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "transaction_id is required", errCodeMissingTransactionID)
		return
	}

	var req UpdateStatusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}

	if !domain.ValidTransactionStatus(req.Status) {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "invalid status", errCodeInvalidStatus)
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String(logging.KeyTransactionID, txnID),
		slog.String("target_status", string(req.Status)),
	)

	updated, err := h.service.UpdateTransactionStatus(r.Context(), txnID, req.Status, req.StatusReason, req.ProviderReference)
	if errors.Is(err, repository.ErrNotFound) {
		httpx.ErrorWithCode(w, http.StatusNotFound, "transaction not found", "TRANSACTION_NOT_FOUND")
		return
	}
	if errors.Is(err, repository.ErrIllegalTransition) {
		httpx.ErrorWithCode(w, http.StatusConflict, err.Error(), "ILLEGAL_TRANSITION")
		return
	}
	if err != nil {
		logger.Error("failed to update transaction status", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	logger.Info("transaction status updated", "new_status", string(updated.Status))
	httpx.JSON(w, http.StatusOK, updated)
}

// GetTransaction handles GET /transactions/{transaction_id}.
// Returns a single transaction by its ID.
func (h *Handler) GetTransaction(w http.ResponseWriter, r *http.Request) {
	txnID := chi.URLParam(r, "transaction_id")
	if txnID == "" {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "transaction_id is required", errCodeMissingTransactionID)
		return
	}

	txn, err := h.service.GetTransaction(r.Context(), txnID)
	if errors.Is(err, repository.ErrNotFound) {
		httpx.ErrorWithCode(w, http.StatusNotFound, "transaction not found", "TRANSACTION_NOT_FOUND")
		return
	}
	if err != nil {
		logging.FromContext(r.Context()).Error("failed to get transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	httpx.JSON(w, http.StatusOK, txn)
}

// ListTransactions handles GET /transactions?user_id={user_id}&limit={limit}&cursor={cursor}.
// Returns a cursor-paginated list of transactions for a user, ordered by (created_at DESC, id DESC).
func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		httpx.ErrorWithCode(w, http.StatusBadRequest, "user_id query parameter is required", errCodeMissingUserID)
		return
	}

	// Parse optional limit.
	limit := domain.DefaultPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			httpx.ErrorWithCode(w, http.StatusBadRequest, "limit must be a positive integer", "INVALID_LIMIT")
			return
		}
		if parsed > domain.MaxPageLimit {
			parsed = domain.MaxPageLimit
		}
		limit = parsed
	}

	// Parse optional cursor.
	var cursor *domain.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		c, err := domain.DecodeCursor(raw)
		if err != nil {
			httpx.ErrorWithCode(w, http.StatusBadRequest, "invalid cursor", "INVALID_CURSOR")
			return
		}
		cursor = c
	}

	result, err := h.service.ListTransactions(r.Context(), domain.ListTransactionsQuery{
		UserID: userID,
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		logging.FromContext(r.Context()).Error("failed to list transactions", "error", err, "user_id", userID)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array instead of null.
	txns := result.Transactions
	if txns == nil {
		txns = []domain.Transaction{}
	}

	resp := map[string]any{
		"user_id":      userID,
		"transactions": txns,
		"has_more":     result.HasMore,
	}
	if result.NextCursor != nil {
		resp["next_cursor"] = *result.NextCursor
	} else {
		resp["next_cursor"] = nil
	}

	httpx.JSON(w, http.StatusOK, resp)
}
