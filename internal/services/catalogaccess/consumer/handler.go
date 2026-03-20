package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/domain"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/catalogaccess/service"
)

// Publisher defines the subset of publishing operations needed by consumers.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// Handler provides AMQP message handlers for catalog-access commands.
type Handler struct {
	service   *service.Service
	publisher Publisher
	logger    *slog.Logger
}

// NewHandler creates a new consumer handler.
func NewHandler(service *service.Service, publisher Publisher, logger *slog.Logger) *Handler {
	return &Handler{service: service, publisher: publisher, logger: logger}
}

// Handle routes an incoming catalog-access command to the matching handler.
func (c *Handler) Handle(ctx context.Context, env messaging.Envelope) error {
	switch env.Type {
	case messaging.RoutingKeyAccessGrantRequested:
		return c.HandleAccessGrantRequested(ctx, env)
	case messaging.RoutingKeyAccessRevokeRequested:
		return c.HandleAccessRevokeRequested(ctx, env)
	default:
		c.logger.Warn("unknown message type", slog.String("type", env.Type))
		return nil
	}
}

// HandleAccessGrantRequested processes an access.grant.requested command.
// It grants access to the specified offering for the user, publishing
// either access.granted or access.grant.conflicted as the outcome.
func (c *Handler) HandleAccessGrantRequested(ctx context.Context, env messaging.Envelope) error {
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

	_, err := c.service.GrantAccess(ctx, cmd.UserID, cmd.OfferingID, cmd.TransactionID)
	if errors.Is(err, repository.ErrDuplicateAccess) {
		existing, lookupErr := c.service.GetActiveAccess(ctx, cmd.UserID, cmd.OfferingID)
		if lookupErr != nil {
			logger.Error("failed to load existing access after duplicate grant", "error", lookupErr)
			return fmt.Errorf("lookup existing access after duplicate grant: %w", lookupErr)
		}

		if existing.TransactionID == cmd.TransactionID {
			logger.Info("access grant already applied, replaying access.granted")
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
func (c *Handler) HandleAccessRevokeRequested(ctx context.Context, env messaging.Envelope) error {
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

	_, err := c.service.RevokeAccess(ctx, cmd.OriginalTransactionID)
	if errors.Is(err, repository.ErrNoActiveAccess) {
		existing, lookupErr := c.service.GetAccessByTransaction(ctx, cmd.OriginalTransactionID)
		if lookupErr == nil && existing.Status == domain.AccessStatusRevoked {
			logger.Info("access revoke already applied, replaying access.revoked")
			return c.publisher.Publish(ctx,
				messaging.ExchangeOutcomes,
				messaging.RoutingKeyAccessRevoked,
				env.CorrelationID,
				messaging.AccessRevoked{
					TransactionID:         cmd.TransactionID,
					OriginalTransactionID: cmd.OriginalTransactionID,
					UserID:                cmd.UserID,
					OfferingID:            cmd.OfferingID,
				},
			)
		}
		if lookupErr != nil && !errors.Is(lookupErr, repository.ErrNotFound) {
			logger.Error("failed to load existing access after revoke miss", "error", lookupErr)
			return fmt.Errorf("lookup existing access after revoke miss: %w", lookupErr)
		}

		logger.Warn("access revoke rejected: no active access found")
		return c.publisher.Publish(ctx,
			messaging.ExchangeOutcomes,
			messaging.RoutingKeyAccessRevokeRejected,
			env.CorrelationID,
			messaging.AccessRevokeRejected{
				TransactionID:         cmd.TransactionID,
				OriginalTransactionID: cmd.OriginalTransactionID,
				UserID:                cmd.UserID,
				OfferingID:            cmd.OfferingID,
				Reason:                "no active access found for this transaction",
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
			TransactionID:         cmd.TransactionID,
			OriginalTransactionID: cmd.OriginalTransactionID,
			UserID:                cmd.UserID,
			OfferingID:            cmd.OfferingID,
		},
	)
}
