package consumer

import (
	"context"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/handleoutcome"
)

// Handler provides AMQP message handlers for saga outcomes.
type Handler struct {
	handleOutcome *handleoutcome.UseCase
}

// NewHandler creates a new consumer handler.
func NewHandler(
	repo repository.Repository,
	paymentsClient client.PaymentsClient,
	publisher activities.Publisher,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		handleOutcome: handleoutcome.New(repo, paymentsClient, publisher, logger),
	}
}

// Handle routes an incoming saga outcome event to the outcome handler.
func (h *Handler) Handle(ctx context.Context, env messaging.Envelope) error {
	return h.HandleOutcome(ctx, env)
}

// HandleOutcome routes an incoming outcome event to the outcome use case.
func (h *Handler) HandleOutcome(ctx context.Context, env messaging.Envelope) error {
	return h.handleOutcome.Execute(ctx, env)
}
