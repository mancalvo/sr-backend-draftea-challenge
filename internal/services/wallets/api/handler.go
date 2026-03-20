package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/service"
)

// BalanceResponse is the public response for GET /wallets/{user_id}/balance.
type BalanceResponse struct {
	UserID   string `json:"user_id"`
	Balance  int64  `json:"balance"`
	Currency string `json:"currency"`
}

// Handler provides HTTP handlers for the wallets service.
type Handler struct {
	service *service.Service
	logger  *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(service *service.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// GetBalance handles GET /wallets/{user_id}/balance.
func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id is required")
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", userID),
	)

	wallet, err := h.service.GetBalance(r.Context(), userID)
	if errors.Is(err, repository.ErrWalletNotFound) {
		httpx.ErrorWithCode(w, http.StatusNotFound, "wallet not found", "WALLET_NOT_FOUND")
		return
	}
	if err != nil {
		logger.Error("failed to get wallet", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}

	httpx.JSON(w, http.StatusOK, BalanceResponse{
		UserID:   wallet.UserID,
		Balance:  wallet.Balance,
		Currency: wallet.Currency,
	})
}
