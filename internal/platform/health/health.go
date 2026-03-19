// Package health provides a reusable GET /health handler for all services.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
)

// Checker is a named dependency that can report its readiness.
type Checker interface {
	// Name returns the dependency name (e.g. "postgres", "rabbitmq").
	Name() string
	// Check returns nil if the dependency is healthy, or an error describing the failure.
	Check(ctx context.Context) error
}

// Status is the response body for the health endpoint.
type Status struct {
	Status  string            `json:"status"`
	Service string            `json:"service"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// Handler returns an http.HandlerFunc that responds with a health check.
// Optional Checker values are probed; if any fail the response status is 503.
func Handler(service string, checkers ...Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		code := http.StatusOK

		var checks map[string]string
		if len(checkers) > 0 {
			checks = make(map[string]string, len(checkers))
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			for _, c := range checkers {
				if err := c.Check(ctx); err != nil {
					checks[c.Name()] = err.Error()
					status = "degraded"
					code = http.StatusServiceUnavailable
				} else {
					checks[c.Name()] = "ok"
				}
			}
		}

		httpx.JSON(w, code, Status{
			Status:  status,
			Service: service,
			Checks:  checks,
		})
	}
}

// DBPinger wraps a *sql.DB (or anything with PingContext) as a Checker.
type DBPinger struct {
	Pinger interface {
		PingContext(ctx context.Context) error
	}
}

func (d *DBPinger) Name() string                    { return "postgres" }
func (d *DBPinger) Check(ctx context.Context) error { return d.Pinger.PingContext(ctx) }

// RabbitMQChecker wraps a rabbitmq.Connection-like value as a Checker.
type RabbitMQChecker struct {
	Conn interface {
		IsClosed() bool
	}
}

func (r *RabbitMQChecker) Name() string { return "rabbitmq" }
func (r *RabbitMQChecker) Check(_ context.Context) error {
	if r.Conn.IsClosed() {
		return errConnectionClosed
	}
	return nil
}

type constError string

func (e constError) Error() string { return string(e) }

const errConnectionClosed = constError("connection closed")
