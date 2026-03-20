package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

const serviceName = "wallets"

// NewRouter creates the HTTP router for the wallets service.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer)

	r.Get("/health", health.Handler(serviceName, checkers...))
	r.Get("/wallets/{user_id}/balance", handler.GetBalance)

	return r
}
