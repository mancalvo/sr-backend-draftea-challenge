// Package health provides a reusable GET /health handler for all services.
package health

import (
	"net/http"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

// Status is the response body for the health endpoint.
type Status struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// Handler returns an http.HandlerFunc that responds with a health check.
func Handler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, Status{
			Status:  "ok",
			Service: service,
		})
	}
}
