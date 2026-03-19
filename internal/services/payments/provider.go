package payments

import (
	"context"
	"time"
)

// ChargeResult holds the outcome of a provider charge attempt.
type ChargeResult struct {
	Success     bool
	ProviderRef string
	Reason      string
}

// Provider defines the abstraction for an external payment provider.
// Implementations may call real external APIs or simulate behavior for testing.
type Provider interface {
	// Charge initiates a charge against the external provider.
	// Returns a ChargeResult indicating success or failure.
	Charge(ctx context.Context, transactionID, userID string, amount int64, currency string) (*ChargeResult, error)
}

// SimulatedProvider is a provider implementation that always succeeds after a short delay.
// Suitable for development and testing without a real external provider.
type SimulatedProvider struct {
	Delay time.Duration
}

// NewSimulatedProvider creates a SimulatedProvider with the given simulated processing delay.
func NewSimulatedProvider(delay time.Duration) *SimulatedProvider {
	return &SimulatedProvider{Delay: delay}
}

func (p *SimulatedProvider) Charge(ctx context.Context, transactionID, _ string, _ int64, _ string) (*ChargeResult, error) {
	select {
	case <-time.After(p.Delay):
		return &ChargeResult{
			Success:     true,
			ProviderRef: "sim-" + transactionID,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
