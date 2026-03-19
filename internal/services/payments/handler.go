package payments

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

// Handler provides HTTP handlers for the payments service.
type Handler struct {
	repo   Repository
	logger *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return &Handler{repo: repo, logger: logger}
}

// CreateTransaction handles POST /internal/transactions.
// Registers a new transaction in pending status. The transaction_id is provided
// by the caller (saga-orchestrator generates it via payments service).
func (h *Handler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	var req CreateTransactionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.ID == "" || req.UserID == "" || req.Amount <= 0 {
		httpx.Error(w, http.StatusBadRequest, "id, user_id, and a positive amount are required")
		return
	}
	if !ValidTransactionType(req.Type) {
		httpx.Error(w, http.StatusBadRequest, "invalid transaction type")
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

	txn := &Transaction{
		ID:         req.ID,
		UserID:     req.UserID,
		Type:       req.Type,
		Status:     StatusPending,
		Amount:     req.Amount,
		Currency:   req.Currency,
		OfferingID: req.OfferingID,
	}

	created, err := h.repo.CreateTransaction(r.Context(), txn)
	if errors.Is(err, ErrDuplicateID) {
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
		httpx.Error(w, http.StatusBadRequest, "transaction_id is required")
		return
	}

	var req UpdateStatusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if !ValidTransactionStatus(req.Status) {
		httpx.Error(w, http.StatusBadRequest, "invalid status")
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String(logging.KeyTransactionID, txnID),
		slog.String("target_status", string(req.Status)),
	)

	updated, err := h.repo.UpdateTransactionStatus(r.Context(), txnID, req.Status, req.StatusReason)
	if errors.Is(err, ErrNotFound) {
		httpx.ErrorWithCode(w, http.StatusNotFound, "transaction not found", "TRANSACTION_NOT_FOUND")
		return
	}
	if errors.Is(err, ErrIllegalTransition) {
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
		httpx.Error(w, http.StatusBadRequest, "transaction_id is required")
		return
	}

	txn, err := h.repo.GetTransactionByID(r.Context(), txnID)
	if errors.Is(err, ErrNotFound) {
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

// ListTransactions handles GET /transactions?user_id={user_id}.
// Returns all transactions for a user, ordered by creation time descending.
func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id query parameter is required")
		return
	}

	txns, err := h.repo.ListTransactionsByUserID(r.Context(), userID)
	if err != nil {
		logging.FromContext(r.Context()).Error("failed to list transactions", "error", err, "user_id", userID)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array instead of null.
	if txns == nil {
		txns = []Transaction{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"user_id":      userID,
		"transactions": txns,
	})
}
