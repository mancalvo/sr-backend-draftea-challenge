package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/service"
)

// Handler provides HTTP handlers for the catalog-access service.
type Handler struct {
	service *service.Service
	logger  *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(service *service.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
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
	entitlements, err := h.service.GetEntitlements(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		httpx.ErrorWithCode(w, http.StatusNotFound, "user not found", "USER_NOT_FOUND")
		return
	}
	if err != nil {
		logger.Error("failed to list entitlements", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Return empty array instead of null.
	if entitlements == nil {
		entitlements = []domain.Entitlement{}
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
	result, err := h.service.PurchasePrecheck(r.Context(), req.UserID, req.OfferingID)
	if err != nil {
		logger.Error("purchase precheck: check active access failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
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
	result, err := h.service.RefundPrecheck(r.Context(), req.UserID, req.OfferingID, req.TransactionID)
	if err != nil {
		logger.Error("refund precheck failed", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
