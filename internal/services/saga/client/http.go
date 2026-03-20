package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

// CatalogClient makes synchronous HTTP calls to the catalog-access service
// for purchase and refund prechecks.
type CatalogClient interface {
	// PurchasePrecheck validates that a purchase can proceed.
	PurchasePrecheck(ctx context.Context, userID, offeringID string) (*PrecheckResult, error)

	// RefundPrecheck validates that a refund can proceed.
	RefundPrecheck(ctx context.Context, userID, offeringID, transactionID string) (*PrecheckResult, error)
}

// PrecheckResult mirrors the catalog-access precheck response.
type PrecheckResult struct {
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
	Price    int64  `json:"price,omitempty"`
	Currency string `json:"currency,omitempty"`
}

// PaymentsClient makes synchronous HTTP calls to the payments service
// for transaction registration and status updates.
type PaymentsClient interface {
	// RegisterTransaction creates a new pending transaction.
	RegisterTransaction(ctx context.Context, req RegisterTransactionRequest) (*RegisterTransactionResponse, error)

	// GetTransaction retrieves an existing transaction by ID.
	GetTransaction(ctx context.Context, transactionID string) (*TransactionDetails, error)

	// UpdateTransactionStatus transitions a transaction to a new status.
	UpdateTransactionStatus(ctx context.Context, transactionID string, status string, reason *string, providerReference *string) error
}

// RegisterTransactionRequest is the body for POST /internal/transactions on payments.
type RegisterTransactionRequest struct {
	ID                    string  `json:"id"`
	UserID                string  `json:"user_id"`
	Type                  string  `json:"type"`
	Amount                int64   `json:"amount"`
	Currency              string  `json:"currency"`
	OfferingID            *string `json:"offering_id,omitempty"`
	OriginalTransactionID *string `json:"original_transaction_id,omitempty"`
}

// RegisterTransactionResponse is the parsed response from transaction registration.
type RegisterTransactionResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// TransactionDetails mirrors the payments transaction response needed by saga.
type TransactionDetails struct {
	ID                    string  `json:"id"`
	UserID                string  `json:"user_id"`
	Type                  string  `json:"type"`
	Status                string  `json:"status"`
	Amount                int64   `json:"amount"`
	Currency              string  `json:"currency"`
	OfferingID            *string `json:"offering_id,omitempty"`
	OriginalTransactionID *string `json:"original_transaction_id,omitempty"`
	ProviderReference     *string `json:"provider_reference,omitempty"`
}

// ---- HTTP implementations ----

// HTTPCatalogClient is the real HTTP implementation of CatalogClient.
type HTTPCatalogClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPCatalogClient creates a new client for catalog-access.
func NewHTTPCatalogClient(baseURL string, timeout time.Duration) *HTTPCatalogClient {
	return &HTTPCatalogClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *HTTPCatalogClient) PurchasePrecheck(ctx context.Context, userID, offeringID string) (*PrecheckResult, error) {
	body, err := json.Marshal(map[string]string{
		"user_id":     userID,
		"offering_id": offeringID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal precheck request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/purchase-precheck", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	propagateCorrelationID(req)

	return c.doPrecheck(req)
}

func (c *HTTPCatalogClient) RefundPrecheck(ctx context.Context, userID, offeringID, transactionID string) (*PrecheckResult, error) {
	body, err := json.Marshal(map[string]string{
		"user_id":        userID,
		"offering_id":    offeringID,
		"transaction_id": transactionID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal precheck request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/refund-precheck", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	propagateCorrelationID(req)

	return c.doPrecheck(req)
}

func (c *HTTPCatalogClient) doPrecheck(req *http.Request) (*PrecheckResult, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("precheck request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("precheck returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Success bool           `json:"success"`
		Data    PrecheckResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode precheck response: %w", err)
	}
	return &envelope.Data, nil
}

// HTTPPaymentsClient is the real HTTP implementation of PaymentsClient.
type HTTPPaymentsClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPPaymentsClient creates a new client for the payments service.
func NewHTTPPaymentsClient(baseURL string, timeout time.Duration) *HTTPPaymentsClient {
	return &HTTPPaymentsClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *HTTPPaymentsClient) RegisterTransaction(ctx context.Context, req RegisterTransactionRequest) (*RegisterTransactionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal register request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/transactions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	propagateCorrelationID(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("register transaction request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("register transaction returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Success bool                        `json:"success"`
		Data    RegisterTransactionResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode register response: %w", err)
	}
	return &envelope.Data, nil
}

func (c *HTTPPaymentsClient) GetTransaction(ctx context.Context, transactionID string) (*TransactionDetails, error) {
	url := fmt.Sprintf("%s/transactions/%s", c.baseURL, transactionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	propagateCorrelationID(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get transaction request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get transaction returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Success bool               `json:"success"`
		Data    TransactionDetails `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode get transaction response: %w", err)
	}
	return &envelope.Data, nil
}

func (c *HTTPPaymentsClient) UpdateTransactionStatus(ctx context.Context, transactionID string, status string, reason *string, providerReference *string) error {
	payload := map[string]any{
		"status": status,
	}
	if reason != nil {
		payload["status_reason"] = *reason
	}
	if providerReference != nil {
		payload["provider_reference"] = *providerReference
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal status update: %w", err)
	}

	url := fmt.Sprintf("%s/internal/transactions/%s/status", c.baseURL, transactionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	propagateCorrelationID(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update transaction status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update transaction status returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func propagateCorrelationID(req *http.Request) {
	if correlationID := logging.CorrelationIDFromContext(req.Context()); correlationID != "" {
		req.Header.Set(httpx.HeaderCorrelationID, correlationID)
	}
}
