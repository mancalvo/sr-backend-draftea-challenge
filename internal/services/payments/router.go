package payments

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

const serviceName = "payments"

// NewRouter creates the HTTP router for the payments service.
// Optional health.Checker values are probed by the /health endpoint.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer)

	// Health check
	r.Get("/health", health.Handler(serviceName, checkers...))

	// Public endpoints
	r.Get("/transactions/{transaction_id}", handler.GetTransaction)
	r.Get("/transactions", handler.ListTransactions)

	// Internal endpoints
	r.Post("/internal/transactions", handler.CreateTransaction)
	r.Patch("/internal/transactions/{transaction_id}/status", handler.UpdateTransactionStatus)

	return r
}
