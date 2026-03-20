package consumer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

// Handler provides AMQP message handlers for payments commands.
type Handler struct {
	processDeposit *processdeposit.UseCase
	logger         *slog.Logger
}

// NewHandler creates a new consumer handler.
func NewHandler(processDeposit *processdeposit.UseCase, logger *slog.Logger) *Handler {
	return &Handler{processDeposit: processDeposit, logger: logger}
}

// Handle routes an incoming payments command to the matching handler.
func (h *Handler) Handle(ctx context.Context, env messaging.Envelope) error {
	switch env.Type {
	case messaging.RoutingKeyDepositRequested:
		return h.HandleDepositRequested(ctx, env)
	default:
		h.logger.Warn("unknown message type", slog.String("type", env.Type))
		return nil
	}
}

// HandleDepositRequested processes a payments.deposit.requested command.
func (h *Handler) HandleDepositRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.DepositRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode payments.deposit.requested: %w", err)
	}
	return h.processDeposit.Execute(ctx, env, cmd)
}
