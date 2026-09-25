// Package brave implements the native web search tool backed by the Brave
// Search API.
package brave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumResponseBytes = 4 << 20

// Config contains the subscription token and endpoint configuration used by the
// Brave Search client. The endpoint override keeps the client fully testable
// without external network access.
type Config struct {
	SubscriptionToken string
	APIURL            string
	CallTimeout       time.Duration
}

// Client performs authenticated Brave Search API calls. Brave uses a static
// subscription token, so no token refresh or caching is required.
type Client struct {
	subscriptionToken string
	apiURL            string
	httpClient        *http.Client
}

// NewClient validates configuration and creates a reusable Brave Search client.
func NewClient(configuration Config) (*Client, error) {
	if strings.TrimSpace(configuration.SubscriptionToken) == "" {
		return nil, fmt.Errorf("Brave subscription token is required")
	}
	if err := validateBaseURL(configuration.APIURL, "Brave API URL"); err != nil {
		return nil, err
	}
	if configuration.CallTimeout <= 0 {
		return nil, fmt.Errorf("Brave call timeout must be positive")
	}

	return &Client{
		subscriptionToken: strings.TrimSpace(configuration.SubscriptionToken),
		apiURL:            strings.TrimRight(configuration.APIURL, "/"),
		httpClient:        &http.Client{Timeout: configuration.CallTimeout},
	}, nil
}

// get performs one authenticated GET request against the Brave Search API and
// returns its validated JSON body.
func (client *Client) get(applicationContext context.Context, apiPath string, queryValues url.Values) ([]byte, error) {
	requestURL := client.apiURL + apiPath
	if len(queryValues) != 0 {
		requestURL += "?" + queryValues.Encode()
	}

	request, err := http.NewRequestWithContext(applicationContext, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create Brave Search request: %w", err)
	}
	request.Header.Set("X-Subscription-Token", client.subscriptionToken)
	request.Header.Set("Accept", "application/json")
	// Accept-Encoding is deliberately left to the transport. Setting it here
	// would disable Go's transparent gzip decompression and leave the Brave
	// response body compressed.

	response, err := client.httpClient.Do(request)
	if err != nil {
		var requestURLError *url.Error
		if errors.As(err, &requestURLError) {
			err = requestURLError.Err
		}
		return nil, fmt.Errorf("call Brave Search API: %w", err)
	}
	responseBody, readError := readLimitedResponse(response.Body)
	closeError := response.Body.Close()
	if readError != nil {
		return nil, fmt.Errorf("read Brave Search API response: %w", readError)
	}
	if closeError != nil {
		return nil, fmt.Errorf("close Brave Search API response: %w", closeError)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, apiResponseError("Brave Search API", response.StatusCode, responseBody)
	}
	if !json.Valid(responseBody) {
		return nil, fmt.Errorf("Brave Search API returned invalid JSON")
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

// readLimitedResponse prevents an upstream response from consuming unbounded
// memory while allowing normal Brave result payloads.
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

// apiResponseError keeps useful Brave error identifiers in tool output without
// ever including the subscription token.
func apiResponseError(serviceName string, statusCode int, responseBody []byte) error {
	errorResponse := struct {
		Type  string `json:"type"`
		Error struct {
			ID     string `json:"id"`
			Code   string `json:"code"`
			Detail string `json:"detail"`
		} `json:"error"`
	}{}
	if json.Unmarshal(responseBody, &errorResponse) == nil {
		errorCode := strings.TrimSpace(errorResponse.Error.Code)
		errorDetail := strings.TrimSpace(errorResponse.Error.Detail)
		if errorCode != "" && errorDetail != "" {
			return fmt.Errorf("%s returned HTTP %d (%s): %s", serviceName, statusCode, errorCode, errorDetail)
		}
		if errorCode != "" {
			return fmt.Errorf("%s returned HTTP %d: %s", serviceName, statusCode, errorCode)
		}
		if errorDetail != "" {
			return fmt.Errorf("%s returned HTTP %d: %s", serviceName, statusCode, errorDetail)
		}
	}
	return fmt.Errorf("%s returned HTTP %d", serviceName, statusCode)
}
