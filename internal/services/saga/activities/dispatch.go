package activities

import (
	"context"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
)

// Publisher defines the subset of publishing operations needed by saga activities.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey, correlationID string, payload any) error
}

// UpdateTransactionStatus persists a transaction status change in payments.
func UpdateTransactionStatus(ctx context.Context, paymentsClient client.PaymentsClient, transactionID string, status string, reason *string, providerReference *string) error {
	return paymentsClient.UpdateTransactionStatus(ctx, transactionID, status, reason, providerReference)
}

// PublishDepositRequested publishes the initial deposit command.
func PublishDepositRequested(ctx context.Context, publisher Publisher, correlationID string, payload messaging.DepositRequested) error {
	return publisher.Publish(ctx, messaging.ExchangeCommands, messaging.RoutingKeyDepositRequested, correlationID, payload)
}

// PublishWalletDebitRequested publishes a wallet debit command.
func PublishWalletDebitRequested(ctx context.Context, publisher Publisher, correlationID string, payload messaging.WalletDebitRequested) error {
	return publisher.Publish(ctx, messaging.ExchangeCommands, messaging.RoutingKeyWalletDebitRequested, correlationID, payload)
}

// PublishWalletCreditRequested publishes a wallet credit command.
func PublishWalletCreditRequested(ctx context.Context, publisher Publisher, correlationID string, payload messaging.WalletCreditRequested) error {
	return publisher.Publish(ctx, messaging.ExchangeCommands, messaging.RoutingKeyWalletCreditRequested, correlationID, payload)
}

// PublishAccessGrantRequested publishes an access grant command.
func PublishAccessGrantRequested(ctx context.Context, publisher Publisher, correlationID string, payload messaging.AccessGrantRequested) error {
	return publisher.Publish(ctx, messaging.ExchangeCommands, messaging.RoutingKeyAccessGrantRequested, correlationID, payload)
}

// PublishAccessRevokeRequested publishes an access revoke command.
func PublishAccessRevokeRequested(ctx context.Context, publisher Publisher, correlationID string, payload messaging.AccessRevokeRequested) error {
	return publisher.Publish(ctx, messaging.ExchangeCommands, messaging.RoutingKeyAccessRevokeRequested, correlationID, payload)
}
