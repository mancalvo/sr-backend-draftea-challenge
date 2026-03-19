package catalogaccess

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/health"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

const serviceName = "catalog-access"

// NewRouter creates the HTTP router for the catalog-access service.
// Optional health.Checker values are probed by the /health endpoint.
func NewRouter(handler *Handler, logger *slog.Logger, checkers ...health.Checker) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(httpx.RequestLogger(logger))
	r.Use(httpx.Recoverer)

	// Health check
	r.Get("/health", health.Handler(serviceName, checkers...))

	// Public endpoint
	r.Get("/users/{user_id}/entitlements", handler.GetEntitlements)

	// Internal endpoints
	r.Post("/internal/purchase-precheck", handler.PurchasePrecheck)
	r.Post("/internal/refund-precheck", handler.RefundPrecheck)

	return r
}
