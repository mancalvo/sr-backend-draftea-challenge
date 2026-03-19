package messaging

// Exchange names.
const (
	ExchangeCommands = "workflow.commands"
	ExchangeOutcomes = "workflow.outcomes"
	ExchangeDLX      = "workflow.dlx"
)

// Queue names.
const (
	QueuePaymentsCommands      = "payments.commands"
	QueueWalletsCommands       = "wallets.commands"
	QueueCatalogAccessCommands = "catalog_access.commands"
	QueueSagaOutcomes          = "saga.outcomes"
)

// Command routing keys.
const (
	RoutingKeyDepositRequested      = "payments.deposit.requested"
	RoutingKeyWalletDebitRequested  = "wallet.debit.requested"
	RoutingKeyWalletCreditRequested = "wallet.credit.requested"
	RoutingKeyAccessGrantRequested  = "access.grant.requested"
	RoutingKeyAccessRevokeRequested = "access.revoke.requested"
)

// Outcome routing keys.
const (
	RoutingKeyWalletDebited           = "wallet.debited"
	RoutingKeyWalletDebitRejected     = "wallet.debit.rejected"
	RoutingKeyWalletCredited          = "wallet.credited"
	RoutingKeyAccessGranted           = "access.granted"
	RoutingKeyAccessGrantConflicted   = "access.grant.conflicted"
	RoutingKeyAccessRevoked           = "access.revoked"
	RoutingKeyAccessRevokeRejected    = "access.revoke.rejected"
	RoutingKeyProviderChargeSucceeded = "provider.charge.succeeded"
	RoutingKeyProviderChargeFailed    = "provider.charge.failed"
)
