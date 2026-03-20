package payments

import (
	"context"
	"sync"
	"time"
)

// ChargeResult holds the outcome of a provider charge attempt.
type ChargeResult struct {
	Success     bool
	ProviderRef string
	Reason      string
}

// Provider defines the abstraction for an external payment provider.
// Implementations must treat transactionID as an idempotency key.
type Provider interface {
	// Charge initiates a charge against the external provider.
	// Returns a ChargeResult indicating success or failure.
	Charge(ctx context.Context, transactionID, userID string, amount int64, currency string) (*ChargeResult, error)
}

// SimulatedProvider is a provider implementation that always succeeds after a short delay.
// Suitable for development and testing without a real external provider.
type SimulatedProvider struct {
	Delay time.Duration
	mu    sync.Mutex
	cache map[string]*ChargeResult
}

// NewSimulatedProvider creates a SimulatedProvider with the given simulated processing delay.
func NewSimulatedProvider(delay time.Duration) *SimulatedProvider {
	return &SimulatedProvider{
		Delay: delay,
		cache: make(map[string]*ChargeResult),
	}
}

func (p *SimulatedProvider) Charge(ctx context.Context, transactionID, _ string, _ int64, _ string) (*ChargeResult, error) {
	p.mu.Lock()
	if cached, ok := p.cache[transactionID]; ok {
		result := *cached
		p.mu.Unlock()
		return &result, nil
	}
	p.mu.Unlock()

	select {
	case <-time.After(p.Delay):
		result := &ChargeResult{
			Success:     true,
			ProviderRef: "sim-" + transactionID,
		}
		p.mu.Lock()
		p.cache[transactionID] = result
		p.mu.Unlock()
		return &ChargeResult{
			Success:     result.Success,
			ProviderRef: result.ProviderRef,
			Reason:      result.Reason,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
