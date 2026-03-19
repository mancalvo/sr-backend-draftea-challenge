package wallets

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

// Handler provides HTTP handlers for the wallets service.
type Handler struct {
	repo   Repository
	logger *slog.Logger
}

// NewHandler creates a new Handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return &Handler{repo: repo, logger: logger}
}

// GetBalance handles GET /wallets/{user_id}/balance.
// Returns the current balance for the specified user's wallet.
func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		httpx.Error(w, http.StatusBadRequest, "user_id is required")
		return
	}

	logger := logging.FromContext(r.Context()).With(
		slog.String("user_id", userID),
	)

	wallet, err := h.repo.GetWalletByUserID(r.Context(), userID)
	if errors.Is(err, ErrWalletNotFound) {
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
