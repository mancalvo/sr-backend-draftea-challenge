package provider

import (
	"context"
	"testing"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
)

func TestMockProvider_HonorsControlsWhenEnabled(t *testing.T) {
	provider := NewMockProvider(5*time.Second, true)

	start := time.Now()
	result, err := provider.Charge(context.Background(), messaging.DepositRequested{
		TransactionID: "txn-1",
		UserID:        "user-1",
		Amount:        100,
		Currency:      "ARS",
		MockProvider: &messaging.MockProviderControls{
			Delay:  "0s",
			Result: "fail",
		},
	})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if result.Success {
		t.Fatal("expected forced failure result")
	}
	if result.Reason != "forced failure" {
		t.Fatalf("reason = %q, want forced failure", result.Reason)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("charge took too long: %s", elapsed)
	}
}

func TestMockProvider_IgnoresControlsWhenDisabled(t *testing.T) {
	provider := NewMockProvider(0, false)

	result, err := provider.Charge(context.Background(), messaging.DepositRequested{
		TransactionID: "txn-2",
		UserID:        "user-1",
		Amount:        100,
		Currency:      "ARS",
		MockProvider: &messaging.MockProviderControls{
			Result: "fail",
		},
	})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if !result.Success {
		t.Fatal("expected controls to be ignored when disabled")
	}
}
