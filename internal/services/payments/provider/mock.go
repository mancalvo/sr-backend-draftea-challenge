package provider

import (
	"context"
	"sync"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/payments/usecases/processdeposit"
)

// MockProvider is a provider implementation that always succeeds after a short delay.
// Suitable for development and testing without a real external provider.
type MockProvider struct {
	Delay          time.Duration
	EnableControls bool
	mu             sync.Mutex
	cache          map[string]*processdeposit.ChargeResult
}

// NewMockProvider creates a mock provider with the given simulated processing delay.
func NewMockProvider(delay time.Duration, enableControls bool) *MockProvider {
	return &MockProvider{
		Delay:          delay,
		EnableControls: enableControls,
		cache:          make(map[string]*processdeposit.ChargeResult),
	}
}

func (p *MockProvider) Charge(ctx context.Context, cmd messaging.DepositRequested) (*processdeposit.ChargeResult, error) {
	p.mu.Lock()
	if cached, ok := p.cache[cmd.TransactionID]; ok {
		result := *cached
		p.mu.Unlock()
		return &result, nil
	}
	p.mu.Unlock()

	delay := p.Delay
	result := &processdeposit.ChargeResult{
		Success:     true,
		ProviderRef: "sim-" + cmd.TransactionID,
	}
	if p.EnableControls && cmd.MockProvider != nil {
		if parsedDelay, err := time.ParseDuration(cmd.MockProvider.Delay); err == nil && parsedDelay >= 0 {
			delay = parsedDelay
		}
		if cmd.MockProvider.Result == "fail" {
			result = &processdeposit.ChargeResult{
				Success: false,
				Reason:  "forced failure",
			}
		}
	}

	select {
	case <-time.After(delay):
		p.mu.Lock()
		p.cache[cmd.TransactionID] = result
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
