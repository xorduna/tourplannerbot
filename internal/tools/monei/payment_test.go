package monei

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunction func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper for an inline test function.
func (roundTrip roundTripFunction) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

// jsonHTTPResponse creates a minimal in-memory JSON HTTP response.
func jsonHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// newTestClient creates a MONEI client with all traffic contained in memory.
func newTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	moneiClient, err := NewClient(Config{
		APIKey:             "test-api-key",
		APIURL:             "https://api.example.com",
		CallTimeout:        time.Second,
		PaymentLinkBaseURL: "https://www.dianabarcelona.com/pay",
	})
	if err != nil {
		t.Fatalf("NewClient returned an error: %v", err)
	}
	moneiClient.httpClient.Transport = transport
	return moneiClient
}

// TestCreatePaymentLinkUsesEURAndReturnsDianaLink verifies the tool safely
// converts the business amount and exposes the canonical public URL.
func TestCreatePaymentLinkUsesEURAndReturnsDianaLink(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/payments" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "test-api-key" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var requestBody map[string]any
		requestBytes, _ := io.ReadAll(request.Body)
		if err := json.Unmarshal(requestBytes, &requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if requestBody["amount"] != float64(12550) || requestBody["currency"] != "EUR" || requestBody["orderId"] != "2034020000000489080" {
			t.Errorf("request body = %#v", requestBody)
		}
		if requestBody["metadata"].(map[string]any)["summary"] != "Tour Gaudí" {
			t.Errorf("metadata = %#v", requestBody["metadata"])
		}
		return jsonHTTPResponse(http.StatusCreated, `{"id":"af6029f80f5fc73a8ad2753eea0b1be0","status":"PENDING"}`), nil
	})
	createPaymentLinkTool, err := NewCreatePaymentLink(newTestClient(t, transport))
	if err != nil {
		t.Fatalf("NewCreatePaymentLink returned an error: %v", err)
	}
	createPaymentLinkTool.now = func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }

	result, err := createPaymentLinkTool.Execute(context.Background(), json.RawMessage(`{"amount":"125.50","customer_email":"client@example.com","customer_name":"Client Example","expiration_date":"2026-10-10","order_id":"2034020000000489080","summary":"Tour Gaudí","allowed_payment_methods":["bizum","card"]}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if !strings.Contains(result, `"payment_link":"https://www.dianabarcelona.com/pay/af6029f80f5fc73a8ad2753eea0b1be0"`) {
		t.Errorf("result = %s", result)
	}
}

// TestGetPaymentUsesOnlyAnOpaqueID verifies full URLs are rejected locally.
func TestGetPaymentUsesOnlyAnOpaqueID(t *testing.T) {
	requestCount := 0
	getPaymentTool, err := NewGetPayment(newTestClient(t, roundTripFunction(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if request.Method != http.MethodGet || request.URL.Path != "/v1/payments/af6029f80f5fc73a8ad2753eea0b1be0" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		return jsonHTTPResponse(http.StatusOK, `{"id":"af6029f80f5fc73a8ad2753eea0b1be0","status":"SUCCEEDED"}`), nil
	})))
	if err != nil {
		t.Fatalf("NewGetPayment returned an error: %v", err)
	}
	if _, err := getPaymentTool.Execute(context.Background(), json.RawMessage(`{"payment_id":"https://www.dianabarcelona.com/pay/af6029f80f5fc73a8ad2753eea0b1be0"}`)); err == nil {
		t.Fatal("Execute returned nil error for a full URL")
	}
	if requestCount != 0 {
		t.Fatalf("request count = %d, want 0", requestCount)
	}
	result, err := getPaymentTool.Execute(context.Background(), json.RawMessage(`{"payment_id":"af6029f80f5fc73a8ad2753eea0b1be0"}`))
	if err != nil || !strings.Contains(result, `"status":"SUCCEEDED"`) {
		t.Fatalf("Execute result = %q, error = %v", result, err)
	}
}
