package workflows

import (
	"fmt"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
)

// OutcomeAction identifies the workflow-owned reaction to an outcome event.
type OutcomeAction string

const (
	ActionPublishAccessGrant        OutcomeAction = "publish_access_grant"
	ActionFailPurchaseDebit         OutcomeAction = "fail_purchase_debit"
	ActionCompletePurchase          OutcomeAction = "complete_purchase"
	ActionStartPurchaseCompensation OutcomeAction = "start_purchase_compensation"
	ActionCompletePurchaseComp      OutcomeAction = "complete_purchase_compensation"
	ActionPublishRefundCredit       OutcomeAction = "publish_refund_credit"
	ActionFailRefundRevoke          OutcomeAction = "fail_refund_revoke"
	ActionCompleteRefund            OutcomeAction = "complete_refund"
	ActionRecordProviderSuccess     OutcomeAction = "record_provider_success"
	ActionFailProviderCharge        OutcomeAction = "fail_provider_charge"
	ActionCompleteDeposit           OutcomeAction = "complete_deposit"
)

// Definition declares which outcome events belong to a saga workflow.
type Definition struct {
	Type           domain.SagaType
	OutcomeActions map[string]OutcomeAction
}

var definitions = map[domain.SagaType]Definition{
	domain.SagaTypeDeposit: {
		Type: domain.SagaTypeDeposit,
		OutcomeActions: map[string]OutcomeAction{
			messaging.RoutingKeyProviderChargeSucceeded: ActionRecordProviderSuccess,
			messaging.RoutingKeyProviderChargeFailed:    ActionFailProviderCharge,
			messaging.RoutingKeyWalletCredited:          ActionCompleteDeposit,
		},
	},
	domain.SagaTypePurchase: {
		Type: domain.SagaTypePurchase,
		OutcomeActions: map[string]OutcomeAction{
			messaging.RoutingKeyWalletDebited:         ActionPublishAccessGrant,
			messaging.RoutingKeyWalletDebitRejected:   ActionFailPurchaseDebit,
			messaging.RoutingKeyAccessGranted:         ActionCompletePurchase,
			messaging.RoutingKeyAccessGrantConflicted: ActionStartPurchaseCompensation,
			messaging.RoutingKeyWalletCredited:        ActionCompletePurchaseComp,
		},
	},
	domain.SagaTypeRefund: {
		Type: domain.SagaTypeRefund,
		OutcomeActions: map[string]OutcomeAction{
			messaging.RoutingKeyAccessRevoked:        ActionPublishRefundCredit,
			messaging.RoutingKeyAccessRevokeRejected: ActionFailRefundRevoke,
			messaging.RoutingKeyWalletCredited:       ActionCompleteRefund,
		},
	},
}

// DefinitionFor returns the workflow definition for the saga type.
func DefinitionFor(t domain.SagaType) (Definition, bool) {
	def, ok := definitions[t]
	return def, ok
}

// ActionFor returns the workflow action to run for a saga type and outcome event.
func ActionFor(t domain.SagaType, eventType string) (OutcomeAction, bool) {
	def, ok := DefinitionFor(t)
	if !ok {
		return "", false
	}
	action, ok := def.OutcomeActions[eventType]
	return action, ok
}

type outcomeLocator func(env messaging.Envelope) (string, error)

var outcomeSagaTransactionIDLocators = map[string]outcomeLocator{
	messaging.RoutingKeyWalletDebited: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.WalletDebited](env, func(payload messaging.WalletDebited) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyWalletDebitRejected: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.WalletDebitRejected](env, func(payload messaging.WalletDebitRejected) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyWalletCredited: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.WalletCredited](env, func(payload messaging.WalletCredited) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyAccessGranted: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.AccessGranted](env, func(payload messaging.AccessGranted) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyAccessGrantConflicted: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.AccessGrantConflicted](env, func(payload messaging.AccessGrantConflicted) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyAccessRevoked: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.AccessRevoked](env, func(payload messaging.AccessRevoked) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyAccessRevokeRejected: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.AccessRevokeRejected](env, func(payload messaging.AccessRevokeRejected) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyProviderChargeSucceeded: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.ProviderChargeSucceeded](env, func(payload messaging.ProviderChargeSucceeded) string {
			return payload.TransactionID
		})
	},
	messaging.RoutingKeyProviderChargeFailed: func(env messaging.Envelope) (string, error) {
		return decodeOutcomeTransactionID[messaging.ProviderChargeFailed](env, func(payload messaging.ProviderChargeFailed) string {
			return payload.TransactionID
		})
	},
}

// SagaTransactionIDForOutcome resolves the saga transaction to load for an outcome event.
func SagaTransactionIDForOutcome(env messaging.Envelope) (string, bool, error) {
	locate, ok := outcomeSagaTransactionIDLocators[env.Type]
	if !ok {
		return "", false, nil
	}
	transactionID, err := locate(env)
	if err != nil {
		return "", true, err
	}
	return transactionID, true, nil
}

func decodeOutcomeTransactionID[T any](env messaging.Envelope, extract func(T) string) (string, error) {
	var payload T
	if err := env.DecodePayload(&payload); err != nil {
		return "", fmt.Errorf("decode %s payload: %w", env.Type, err)
	}
	return extract(payload), nil
}
