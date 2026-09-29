// Package monei implements native tools backed by the MONEI Payments API.
package monei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumResponseBytes = 4 << 20

// Config contains the server-side API configuration used by the MONEI client.
type Config struct {
	APIKey             string
	APIURL             string
	CallTimeout        time.Duration
	PaymentLinkBaseURL string
}

// Client performs authenticated MONEI Payments API calls.
type Client struct {
	apiKey             string
	apiURL             string
	httpClient         *http.Client
	paymentLinkBaseURL string
}

// NewClient validates configuration and creates a reusable MONEI API client.
func NewClient(configuration Config) (*Client, error) {
	if strings.TrimSpace(configuration.APIKey) == "" {
		return nil, fmt.Errorf("MONEI API key is required")
	}
	if err := validateBaseURL(configuration.APIURL, "MONEI API URL"); err != nil {
		return nil, err
	}
	if err := validateBaseURL(configuration.PaymentLinkBaseURL, "MONEI payment link base URL"); err != nil {
		return nil, err
	}
	if configuration.CallTimeout <= 0 {
		return nil, fmt.Errorf("MONEI call timeout must be positive")
	}

	return &Client{
		apiKey:             strings.TrimSpace(configuration.APIKey),
		apiURL:             strings.TrimRight(strings.TrimSpace(configuration.APIURL), "/"),
		httpClient:         &http.Client{Timeout: configuration.CallTimeout},
		paymentLinkBaseURL: strings.TrimRight(strings.TrimSpace(configuration.PaymentLinkBaseURL), "/"),
	}, nil
}

// CreatePayment creates one MONEI payment and returns its API response.
func (client *Client) CreatePayment(applicationContext context.Context, requestBody any) ([]byte, error) {
	return client.doJSON(applicationContext, http.MethodPost, "/v1/payments", requestBody)
}

// GetPayment retrieves one MONEI payment by its opaque payment ID.
func (client *Client) GetPayment(applicationContext context.Context, paymentID string) ([]byte, error) {
	return client.doJSON(applicationContext, http.MethodGet, "/v1/payments/"+url.PathEscape(paymentID), nil)
}

// PaymentLink constructs the configured public payment URL for a MONEI ID.
func (client *Client) PaymentLink(paymentID string) string {
	return client.paymentLinkBaseURL + "/" + paymentID
}

// doJSON performs one authenticated MONEI request and checks the JSON result.
func (client *Client) doJSON(applicationContext context.Context, method, apiPath string, requestBody any) ([]byte, error) {
	var encodedRequestBody []byte
	var err error
	if requestBody != nil {
		encodedRequestBody, err = json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("encode MONEI API request: %w", err)
		}
	}

	request, err := http.NewRequestWithContext(applicationContext, method, client.apiURL+apiPath, bytes.NewReader(encodedRequestBody))
	if err != nil {
		return nil, fmt.Errorf("create MONEI request: %w", err)
	}
	request.Header.Set("Authorization", client.apiKey)
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call MONEI API: %w", err)
	}
	responseBody, readError := readLimitedResponse(response.Body)
	closeError := response.Body.Close()
	if readError != nil {
		return nil, fmt.Errorf("read MONEI API response: %w", readError)
	}
	if closeError != nil {
		return nil, fmt.Errorf("close MONEI API response: %w", closeError)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, apiResponseError(response.StatusCode, responseBody)
	}
	if !json.Valid(responseBody) {
		return nil, fmt.Errorf("MONEI API returned invalid JSON")
	}
	return responseBody, nil
}

// validateBaseURL accepts absolute HTTP endpoints for local tests and HTTPS
// endpoints for production configuration.
func validateBaseURL(rawURL string, fieldName string) error {
	parsedURL, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", fieldName)
	}
	return nil
}

// readLimitedResponse prevents an upstream response from consuming unbounded memory.
func readLimitedResponse(responseBody io.Reader) ([]byte, error) {
	limitedReader := io.LimitReader(responseBody, maximumResponseBytes+1)
	responseBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}
	if len(responseBytes) > maximumResponseBytes {
		return nil, fmt.Errorf("response exceeded %d bytes", maximumResponseBytes)
	}
	return responseBytes, nil
}

// apiResponseError keeps useful MONEI error details without exposing credentials.
func apiResponseError(statusCode int, responseBody []byte) error {
	errorResponse := struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}{}
	if json.Unmarshal(responseBody, &errorResponse) == nil {
		errorCode := strings.TrimSpace(errorResponse.Code)
		if errorCode == "" {
			errorCode = strings.TrimSpace(errorResponse.Error)
		}
		if errorCode != "" && strings.TrimSpace(errorResponse.Message) != "" {
			return fmt.Errorf("MONEI API returned HTTP %d (%s): %s", statusCode, errorCode, errorResponse.Message)
		}
		if errorCode != "" {
			return fmt.Errorf("MONEI API returned HTTP %d: %s", statusCode, errorCode)
		}
	}
	return fmt.Errorf("MONEI API returned HTTP %d", statusCode)
}
