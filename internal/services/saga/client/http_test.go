package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/logging"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestHTTPCatalogClient_PropagatesCorrelationID(t *testing.T) {
	var gotCorrelationID string
	client := &HTTPCatalogClient{
		baseURL: "http://catalog.test",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotCorrelationID = req.Header.Get(httpx.HeaderCorrelationID)
				return jsonResponse(http.StatusOK, `{"success":true,"data":{"allowed":true,"price":1500,"currency":"ARS"}}`), nil
			}),
		},
	}

	ctx := logging.WithCorrelationID(context.Background(), "corr-catalog")
	if _, err := client.PurchasePrecheck(ctx, "user-1", "offering-1"); err != nil {
		t.Fatalf("PurchasePrecheck: %v", err)
	}

	if gotCorrelationID != "corr-catalog" {
		t.Fatalf("correlation ID = %q, want %q", gotCorrelationID, "corr-catalog")
	}
}

func TestHTTPPaymentsClient_PropagatesCorrelationID(t *testing.T) {
	testCases := []struct {
		name string
		call func(ctx context.Context, client *HTTPPaymentsClient) error
	}{
		{
			name: "register transaction",
			call: func(ctx context.Context, client *HTTPPaymentsClient) error {
				_, err := client.RegisterTransaction(ctx, RegisterTransactionRequest{
					ID:       "txn-1",
					UserID:   "user-1",
					Type:     "deposit",
					Amount:   1200,
					Currency: "ARS",
				})
				return err
			},
		},
		{
			name: "get transaction",
			call: func(ctx context.Context, client *HTTPPaymentsClient) error {
				_, err := client.GetTransaction(ctx, "txn-1")
				return err
			},
		},
		{
			name: "update transaction status",
			call: func(ctx context.Context, client *HTTPPaymentsClient) error {
				return client.UpdateTransactionStatus(ctx, "txn-1", "completed", nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var gotCorrelationID string
			client := &HTTPPaymentsClient{
				baseURL: "http://payments.test",
				httpClient: &http.Client{
					Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						gotCorrelationID = req.Header.Get(httpx.HeaderCorrelationID)

						switch {
						case req.Method == http.MethodPost && req.URL.Path == "/internal/transactions":
							return jsonResponse(http.StatusCreated, `{"success":true,"data":{"id":"txn-1","status":"pending"}}`), nil
						case req.Method == http.MethodGet && req.URL.Path == "/transactions/txn-1":
							return jsonResponse(http.StatusOK, `{"success":true,"data":{"id":"txn-1","user_id":"user-1","type":"deposit","status":"pending","amount":1200,"currency":"ARS"}}`), nil
						case req.Method == http.MethodPatch && req.URL.Path == "/internal/transactions/txn-1/status":
							return jsonResponse(http.StatusOK, `{"success":true}`), nil
						default:
							t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
							return nil, nil
						}
					}),
				},
			}

			ctx := logging.WithCorrelationID(context.Background(), "corr-payments")
			if err := tc.call(ctx, client); err != nil {
				t.Fatalf("call failed: %v", err)
			}

			if gotCorrelationID != "corr-payments" {
				t.Fatalf("correlation ID = %q, want %q", gotCorrelationID, "corr-payments")
			}
		})
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
