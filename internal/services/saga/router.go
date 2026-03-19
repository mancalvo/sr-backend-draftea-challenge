package saga

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

const serviceName = "saga-orchestrator"

// NewRouter creates the HTTP router for the saga-orchestrator service.
// Optional health.Checker values are probed by the /health endpoint.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer)

	// Health check
	r.Get("/health", health.Handler(serviceName, checkers...))

	// Command ingress endpoints
	r.Post("/deposits", handler.HandleDeposit)
	r.Post("/purchases", handler.HandlePurchase)
	r.Post("/refunds", handler.HandleRefund)

	return r
}
