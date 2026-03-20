package provider

import (
	"context"
	"sync"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

// MockProvider is a provider implementation that always succeeds after a short delay.
// Suitable for development and testing without a real external provider.
type MockProvider struct {
	Delay time.Duration
	mu    sync.Mutex
	cache map[string]*processdeposit.ChargeResult
}

// NewMockProvider creates a mock provider with the given simulated processing delay.
func NewMockProvider(delay time.Duration) *MockProvider {
	return &MockProvider{
		Delay: delay,
		cache: make(map[string]*processdeposit.ChargeResult),
	}
}

func (p *MockProvider) Charge(ctx context.Context, transactionID, _ string, _ int64, _ string) (*processdeposit.ChargeResult, error) {
	p.mu.Lock()
	if cached, ok := p.cache[transactionID]; ok {
		result := *cached
		p.mu.Unlock()
		return &result, nil
	}
	p.mu.Unlock()

	select {
	case <-time.After(p.Delay):
		result := &processdeposit.ChargeResult{
			Success:     true,
			ProviderRef: "sim-" + transactionID,
		}
		p.mu.Lock()
		p.cache[transactionID] = result
		p.mu.Unlock()
		return &processdeposit.ChargeResult{
			Success:     result.Success,
			ProviderRef: result.ProviderRef,
			Reason:      result.Reason,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
