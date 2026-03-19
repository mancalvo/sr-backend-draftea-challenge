package catalogaccess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

// Publisher defines the subset of publishing operations needed by consumers.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// ConsumerHandler provides AMQP message handlers for catalog-access commands.
type ConsumerHandler struct {
	repo      Repository
	publisher Publisher
	logger    *slog.Logger
}

// NewConsumerHandler creates a new ConsumerHandler.
func NewConsumerHandler(repo Repository, publisher Publisher, logger *slog.Logger) *ConsumerHandler {
	return &ConsumerHandler{repo: repo, publisher: publisher, logger: logger}
}

// HandleAccessGrantRequested processes an access.grant.requested command.
// It grants access to the specified offering for the user, publishing
// either access.granted or access.grant.conflicted as the outcome.
func (c *ConsumerHandler) HandleAccessGrantRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.AccessGrantRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode access.grant.requested: %w", err)
	}

	logger := c.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
	)

	logger.Info("processing access grant request")

	_, err := c.repo.GrantAccess(ctx, cmd.UserID, cmd.OfferingID, cmd.TransactionID)
	if errors.Is(err, ErrDuplicateAccess) {
		logger.Warn("access grant conflicted: user already has active access")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyAccessGrantConflicted,
			env.CorrelationID,
			messaging.AccessGrantConflicted{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				OfferingID:    cmd.OfferingID,
				Reason:        "user already has active access to this offering",
			},
		)
	}
	if err != nil {
		logger.Error("failed to grant access", "error", err)
		return fmt.Errorf("grant access: %w", err)
	}

	logger.Info("access granted successfully")
	return c.publisher.Publish(ctx,
		messaging.ExchangeOutcomes,
		messaging.RoutingKeyAccessGranted,
		env.CorrelationID,
		messaging.AccessGranted{
			TransactionID: cmd.TransactionID,
			UserID:        cmd.UserID,
			OfferingID:    cmd.OfferingID,
		},
	)
}

// HandleAccessRevokeRequested processes an access.revoke.requested command.
// It revokes the active access linked to the transaction, publishing
// either access.revoked or access.revoke.rejected as the outcome.
func (c *ConsumerHandler) HandleAccessRevokeRequested(ctx context.Context, env messaging.Envelope) error {
	var cmd messaging.AccessRevokeRequested
	if err := env.DecodePayload(&cmd); err != nil {
		return fmt.Errorf("decode access.revoke.requested: %w", err)
	}

	logger := c.logger.With(
		slog.String(logging.KeyTransactionID, cmd.TransactionID),
		slog.String(logging.KeyCorrelationID, env.CorrelationID),
		slog.String(logging.KeyMessageID, env.MessageID),
		slog.String("user_id", cmd.UserID),
		slog.String("offering_id", cmd.OfferingID),
	)

	logger.Info("processing access revoke request")

	_, err := c.repo.RevokeAccess(ctx, cmd.TransactionID)
	if errors.Is(err, ErrNoActiveAccess) {
		logger.Warn("access revoke rejected: no active access found")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyAccessRevokeRejected,
			env.CorrelationID,
			messaging.AccessRevokeRejected{
				TransactionID: cmd.TransactionID,
				UserID:        cmd.UserID,
				OfferingID:    cmd.OfferingID,
				Reason:        "no active access found for this transaction",
			},
		)
	}
	if err != nil {
		logger.Error("failed to revoke access", "error", err)
		return fmt.Errorf("revoke access: %w", err)
	}

	logger.Info("access revoked successfully")
	return c.publisher.Publish(ctx,
		messaging.ExchangeOutcomes,
		messaging.RoutingKeyAccessRevoked,
		env.CorrelationID,
		messaging.AccessRevoked{
			TransactionID: cmd.TransactionID,
			UserID:        cmd.UserID,
			OfferingID:    cmd.OfferingID,
		},
	)
}
