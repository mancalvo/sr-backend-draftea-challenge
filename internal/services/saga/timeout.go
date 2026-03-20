package saga

import (
	"context"
	"log/slog"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

// TimeoutConfig holds configuration for the saga timeout poller.
type TimeoutConfig struct {
	// PollInterval is how often the poller checks for timed-out sagas.
	PollInterval time.Duration

	// SagaTimeout is the default duration after which a saga is considered timed out.
	SagaTimeout time.Duration
}

// DefaultTimeoutConfig returns sensible defaults for the timeout poller.
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		PollInterval: 5 * time.Second,
		SagaTimeout:  30 * time.Second,
	}
}

// TimeoutPoller periodically scans running sagas whose timeout_at has passed
// and transitions them to timed_out status. The timeout is persisted in the
// database (not held in memory), so it survives restarts.
type TimeoutPoller struct {
	repo           Repository
	paymentsClient PaymentsClient
	config         TimeoutConfig
	logger         *slog.Logger
}

// NewTimeoutPoller creates a new TimeoutPoller.
func NewTimeoutPoller(
	repo Repository,
	paymentsClient PaymentsClient,
	config TimeoutConfig,
	logger *slog.Logger,
) *TimeoutPoller {
	return &TimeoutPoller{
		repo:           repo,
		paymentsClient: paymentsClient,
		config:         config,
		logger:         logger,
	}
}

// Run starts the polling loop. It blocks until the context is cancelled.
func (p *TimeoutPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	p.logger.Info("timeout poller started",
		slog.Duration("poll_interval", p.config.PollInterval),
		slog.Duration("saga_timeout", p.config.SagaTimeout),
	)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("timeout poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

// poll checks for timed-out sagas and transitions them.
func (p *TimeoutPoller) poll(ctx context.Context) {
	now := time.Now().UTC()
	sagas, err := p.repo.ListTimedOutSagas(ctx, now)
	if err != nil {
		p.logger.Error("failed to list timed-out sagas", "error", err)
		return
	}

	for _, s := range sagas {
		logger := logging.With(p.logger,
			slog.String("saga_id", s.ID),
			slog.String(logging.KeyTransactionID, s.TransactionID),
			slog.String("saga_type", string(s.Type)),
			slog.String("saga_status", string(s.Status)),
		)

		reason := "saga timeout exceeded"
		if err := p.paymentsClient.UpdateTransactionStatus(ctx, s.TransactionID, "timed_out", &reason); err != nil {
			logger.Error("failed to update transaction to timed_out", "error", err)
			continue
		}

		// Transition to timed_out after the ledger update succeeds so retries can
		// safely repair partial progress.
		_, err := p.repo.UpdateSagaStatus(ctx, s.ID, StatusTimedOut, nil, s.CurrentStep)
		if err != nil {
			logger.Error("failed to transition saga to timed_out", "error", err)
		}

		logger.Warn("saga timed out")
	}
}
