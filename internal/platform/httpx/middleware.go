package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/google/uuid"
)

const (
	// HeaderCorrelationID is the HTTP header for propagating correlation IDs.
	HeaderCorrelationID = "X-Correlation-ID"
)

// RequestLogger is middleware that logs every request with method, path,
// status, and duration. It also ensures a correlation ID is present.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			correlationID := r.Header.Get(HeaderCorrelationID)
			if correlationID == "" {
				correlationID = uuid.New().String()
			}
			w.Header().Set(HeaderCorrelationID, correlationID)

			reqLogger := logger.With(
				slog.String(logging.KeyCorrelationID, correlationID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)

			ctx := logging.WithContext(r.Context(), reqLogger)
			ctx = logging.WithCorrelationID(ctx, correlationID)
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(rw, r.WithContext(ctx))

			reqLogger.Info("request completed",
				slog.Int("status", rw.statusCode),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// Recoverer is middleware that recovers from panics and returns 500.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger := logging.FromContext(r.Context())
				logger.Error("panic recovered", slog.Any("panic", rec))
				Error(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
