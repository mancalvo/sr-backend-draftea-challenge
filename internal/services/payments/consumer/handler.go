package consumer

import (
	"context"
	"fmt"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

// Handler provides AMQP message handlers for payments commands.
type Handler struct {
	processDeposit *processdeposit.UseCase
}

// NewHandler creates a new consumer handler.
func NewHandler(processDeposit *processdeposit.UseCase) *Handler {
	return &Handler{processDeposit: processDeposit}
}

// HandleDepositRequested processes a payments.deposit.requested command.
func (h *Handler) HandleDepositRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.DepositRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode payments.deposit.requested: %w", err)
	}
	return h.processDeposit.Execute(ctx, env, cmd)
}
