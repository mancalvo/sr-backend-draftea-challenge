package catalogaccess

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

// Handler provides HTTP handlers for the catalog-access service.
type Handler struct {
	repo   Repository
	logger *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return &Handler{repo: repo, logger: logger}
}

// GetEntitlements handles GET /users/{user_id}/entitlements.
// Returns all active entitlements for a user.
func (h *Handler) GetEntitlements(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id is required")
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String(logging.KeyTransactionID, ""),
		slog.String("user_id", userID),
	)

	// Verify user exists.
	_, err := h.repo.GetUserByID(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		httpx.ErrorWithCode(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		return
	}
	if err != nil {
		logger.Error("failed to get user", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	entitlements, err := h.repo.ListEntitlements(r.Context(), userID)
	if err != nil {
		logger.Error("failed to list entitlements", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array instead of null.
	if entitlements == nil {
		entitlements = []Entitlement{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"user_id":      userID,
		"entitlements": entitlements,
	})
}

// PurchasePrecheck handles POST /internal/purchase-precheck.
// Validates that a purchase can proceed: user exists, offering exists and is active,
// and user does not already have active access to the offering.
func (h *Handler) PurchasePrecheck(w http.ResponseWriter, r *http.Request) {
	var req PurchasePrecheckRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.UserID == "" || req.OfferingID == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id and offering_id are required")
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", req.UserID),
		slog.String("offering_id", req.OfferingID),
	)

	// Check user exists.
	_, err := h.repo.GetUserByID(r.Context(), req.UserID)
	if errors.Is(err, ErrNotFound) {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "user not found"})
		return
	}
	if err != nil {
		logger.Error("purchase precheck: get user failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Check offering exists and is active.
	offering, err := h.repo.GetOfferingByID(r.Context(), req.OfferingID)
	if errors.Is(err, ErrNotFound) {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "offering not found"})
		return
	}
	if err != nil {
		logger.Error("purchase precheck: get offering failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !offering.Active {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "offering is not active"})
		return
	}

	// Check no active access already exists.
	_, err = h.repo.GetActiveAccess(r.Context(), req.UserID, req.OfferingID)
	if err == nil {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "user already has active access to this offering"})
		return
	}
	if !errors.Is(err, ErrNotFound) {
		logger.Error("purchase precheck: check active access failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: true})
}

// RefundPrecheck handles POST /internal/refund-precheck.
// Validates that a refund can proceed: user exists, and there is an active access
// record linked to the given transaction.
func (h *Handler) RefundPrecheck(w http.ResponseWriter, r *http.Request) {
	var req RefundPrecheckRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.UserID == "" || req.OfferingID == "" || req.TransactionID == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id, offering_id, and transaction_id are required")
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", req.UserID),
		slog.String("offering_id", req.OfferingID),
		slog.String(logging.KeyTransactionID, req.TransactionID),
	)

	// Check user exists.
	_, err := h.repo.GetUserByID(r.Context(), req.UserID)
	if errors.Is(err, ErrNotFound) {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "user not found"})
		return
	}
	if err != nil {
		logger.Error("refund precheck: get user failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Check there is an active access record for this transaction.
	access, err := h.repo.GetActiveAccessByTransaction(r.Context(), req.TransactionID)
	if errors.Is(err, ErrNotFound) {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "no active access found for this transaction"})
		return
	}
	if err != nil {
		logger.Error("refund precheck: get access by transaction failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Verify the access record matches the expected user and offering.
	if access.UserID != req.UserID || access.OfferingID != req.OfferingID {
		httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: false, Reason: "access record does not match user and offering"})
		return
	}

	httpx.JSON(w, http.StatusOK, PrecheckResult{Allowed: true})
}
