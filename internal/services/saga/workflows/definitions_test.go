package workflows

import (
	"testing"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/domain"
)

func TestActionFor(t *testing.T) {
	action, ok := ActionFor(domain.SagaTypePurchase, messaging.RoutingKeyAccessGranted)
	if !ok {
		t.Fatal("expected purchase workflow to handle access.granted")
	}
	if action != ActionCompletePurchase {
		t.Fatalf("action = %s, want %s", action, ActionCompletePurchase)
	}

	if _, ok := ActionFor(domain.SagaTypeRefund, messaging.RoutingKeyAccessGranted); ok {
		t.Fatal("refund workflow should not handle access.granted")
	}
}

func TestSagaTransactionIDForOutcome_UsesPayloadTransactionID(t *testing.T) {
	env, err := messaging.NewEnvelope(
		messaging.RoutingKeyWalletDebited,
		"corr-1",
		messaging.WalletDebited{TransactionID: "txn-123"},
	)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}

	transactionID, known, err := SagaTransactionIDForOutcome(env)
	if err != nil {
		t.Fatalf("resolve transaction id: %v", err)
	}
	if !known {
		t.Fatal("expected wallet.debited to be known")
	}
	if transactionID != "txn-123" {
		t.Fatalf("transactionID = %s, want txn-123", transactionID)
	}
}

func TestSagaTransactionIDForOutcome_UsesCorrelationIDForRefundRevoke(t *testing.T) {
	env, err := messaging.NewEnvelope(
		messaging.RoutingKeyAccessRevoked,
		"refund-txn-1",
		messaging.AccessRevoked{TransactionID: "purchase-txn-1"},
	)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}

	transactionID, known, err := SagaTransactionIDForOutcome(env)
	if err != nil {
		t.Fatalf("resolve transaction id: %v", err)
	}
	if !known {
		t.Fatal("expected access.revoked to be known")
	}
	if transactionID != "refund-txn-1" {
		t.Fatalf("transactionID = %s, want refund-txn-1", transactionID)
	}
}
