package messaging

import (
	"encoding/json"
	"testing"
)

// TestContractRoundTrip verifies that every command and outcome contract
// can be serialized into an envelope and decoded back without data loss.
func TestContractRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		routingKey string
		payload    any
		decode     func(Envelope) error
	}{
		{
			name:       "DepositRequested",
			routingKey: RoutingKeyDepositRequested,
			payload:    DepositRequested{TransactionID: "txn-1", UserID: "u-1", Amount: 5000, Currency: "ARS"},
			decode: func(env Envelope) error {
				var p DepositRequested
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "TransactionID", p.TransactionID, "txn-1")
				assertEqual(t, "Amount", p.Amount, int64(5000))
				return nil
			},
		},
		{
			name:       "WalletDebitRequested",
			routingKey: RoutingKeyWalletDebitRequested,
			payload:    WalletDebitRequested{TransactionID: "txn-2", UserID: "u-2", Amount: 3000, Currency: "ARS", SourceStep: "purchase_debit"},
			decode: func(env Envelope) error {
				var p WalletDebitRequested
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "SourceStep", p.SourceStep, "purchase_debit")
				return nil
			},
		},
		{
			name:       "WalletCreditRequested",
			routingKey: RoutingKeyWalletCreditRequested,
			payload:    WalletCreditRequested{TransactionID: "txn-3", UserID: "u-3", Amount: 2000, Currency: "ARS", SourceStep: "deposit_credit"},
			decode: func(env Envelope) error {
				var p WalletCreditRequested
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "SourceStep", p.SourceStep, "deposit_credit")
				return nil
			},
		},
		{
			name:       "AccessGrantRequested",
			routingKey: RoutingKeyAccessGrantRequested,
			payload:    AccessGrantRequested{TransactionID: "txn-4", UserID: "u-4", OfferingID: "off-1"},
			decode: func(env Envelope) error {
				var p AccessGrantRequested
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "OfferingID", p.OfferingID, "off-1")
				return nil
			},
		},
		{
			name:       "AccessRevokeRequested",
			routingKey: RoutingKeyAccessRevokeRequested,
			payload:    AccessRevokeRequested{TransactionID: "txn-5", OriginalTransactionID: "orig-txn-5", UserID: "u-5", OfferingID: "off-2"},
			decode: func(env Envelope) error {
				var p AccessRevokeRequested
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "OfferingID", p.OfferingID, "off-2")
				assertEqual(t, "OriginalTransactionID", p.OriginalTransactionID, "orig-txn-5")
				return nil
			},
		},
		{
			name:       "WalletDebited",
			routingKey: RoutingKeyWalletDebited,
			payload:    WalletDebited{TransactionID: "txn-6", UserID: "u-6", Amount: 1000, BalanceAfter: 4000, SourceStep: "purchase_debit"},
			decode: func(env Envelope) error {
				var p WalletDebited
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "BalanceAfter", p.BalanceAfter, int64(4000))
				return nil
			},
		},
		{
			name:       "WalletDebitRejected",
			routingKey: RoutingKeyWalletDebitRejected,
			payload:    WalletDebitRejected{TransactionID: "txn-7", UserID: "u-7", Amount: 9999, Reason: "insufficient_funds", SourceStep: "purchase_debit"},
			decode: func(env Envelope) error {
				var p WalletDebitRejected
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "Reason", p.Reason, "insufficient_funds")
				return nil
			},
		},
		{
			name:       "WalletCredited",
			routingKey: RoutingKeyWalletCredited,
			payload:    WalletCredited{TransactionID: "txn-8", UserID: "u-8", Amount: 2000, BalanceAfter: 7000, SourceStep: "deposit_credit"},
			decode: func(env Envelope) error {
				var p WalletCredited
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "BalanceAfter", p.BalanceAfter, int64(7000))
				return nil
			},
		},
		{
			name:       "AccessGranted",
			routingKey: RoutingKeyAccessGranted,
			payload:    AccessGranted{TransactionID: "txn-9", UserID: "u-9", OfferingID: "off-3"},
			decode: func(env Envelope) error {
				var p AccessGranted
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "OfferingID", p.OfferingID, "off-3")
				return nil
			},
		},
		{
			name:       "AccessGrantConflicted",
			routingKey: RoutingKeyAccessGrantConflicted,
			payload:    AccessGrantConflicted{TransactionID: "txn-10", UserID: "u-10", OfferingID: "off-4", Reason: "already_active"},
			decode: func(env Envelope) error {
				var p AccessGrantConflicted
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "Reason", p.Reason, "already_active")
				return nil
			},
		},
		{
			name:       "AccessRevoked",
			routingKey: RoutingKeyAccessRevoked,
			payload:    AccessRevoked{TransactionID: "txn-11", OriginalTransactionID: "orig-txn-11", UserID: "u-11", OfferingID: "off-5"},
			decode: func(env Envelope) error {
				var p AccessRevoked
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "OfferingID", p.OfferingID, "off-5")
				assertEqual(t, "OriginalTransactionID", p.OriginalTransactionID, "orig-txn-11")
				return nil
			},
		},
		{
			name:       "AccessRevokeRejected",
			routingKey: RoutingKeyAccessRevokeRejected,
			payload:    AccessRevokeRejected{TransactionID: "txn-12", OriginalTransactionID: "orig-txn-12", UserID: "u-12", OfferingID: "off-6", Reason: "no_active_access"},
			decode: func(env Envelope) error {
				var p AccessRevokeRejected
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "Reason", p.Reason, "no_active_access")
				assertEqual(t, "OriginalTransactionID", p.OriginalTransactionID, "orig-txn-12")
				return nil
			},
		},
		{
			name:       "ProviderChargeSucceeded",
			routingKey: RoutingKeyProviderChargeSucceeded,
			payload:    ProviderChargeSucceeded{TransactionID: "txn-13", UserID: "u-13", Amount: 8000, ProviderRef: "prov-ref-1"},
			decode: func(env Envelope) error {
				var p ProviderChargeSucceeded
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "ProviderRef", p.ProviderRef, "prov-ref-1")
				return nil
			},
		},
		{
			name:       "ProviderChargeFailed",
			routingKey: RoutingKeyProviderChargeFailed,
			payload:    ProviderChargeFailed{TransactionID: "txn-14", UserID: "u-14", Amount: 6000, Reason: "card_declined"},
			decode: func(env Envelope) error {
				var p ProviderChargeFailed
				if err := env.DecodePayload(&p); err != nil {
					return err
				}
				assertEqual(t, "Reason", p.Reason, "card_declined")
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, err := NewEnvelope(tc.routingKey, "corr-test", tc.payload)
			if err != nil {
				t.Fatalf("NewEnvelope: %v", err)
			}

			// Marshal -> Unmarshal round trip.
			data, err := env.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			restored, err := UnmarshalEnvelope(data)
			if err != nil {
				t.Fatalf("UnmarshalEnvelope: %v", err)
			}

			if restored.Type != tc.routingKey {
				t.Errorf("Type = %q, want %q", restored.Type, tc.routingKey)
			}

			if err := tc.decode(restored); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
		})
	}
}

// TestContractJSONFields verifies that JSON field names match the expected
// wire format for a representative contract.
func TestContractJSONFields(t *testing.T) {
	p := WalletDebitRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        1500,
		Currency:      "ARS",
		SourceStep:    "purchase_debit",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	expectedKeys := []string{"transaction_id", "user_id", "amount", "currency", "source_step"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
	if len(m) != len(expectedKeys) {
		t.Errorf("got %d JSON fields, want %d", len(m), len(expectedKeys))
	}
}

// assertEqual is a generic test helper.
func assertEqual[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
