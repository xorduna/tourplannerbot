// Package jina implements native tools backed by the Jina AI Reader API.
package jina

import (
	"bytes"
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

const maximumResponseBytes = 8 << 20

// Config contains the API token and endpoint configuration used by the Jina
// Reader client. The endpoint override keeps the client fully testable without
// external network access.
type Config struct {
	APIToken    string
	ReaderURL   string
	CallTimeout time.Duration
}

// Client performs authenticated Jina Reader API calls. Jina uses a static
// bearer token, so no token refresh or caching is required.
type Client struct {
	apiToken   string
	readerURL  string
	httpClient *http.Client
}

// NewClient validates configuration and creates a reusable Jina Reader client.
func NewClient(configuration Config) (*Client, error) {
	if strings.TrimSpace(configuration.APIToken) == "" {
		return nil, fmt.Errorf("Jina API token is required")
	}
	if err := validateBaseURL(configuration.ReaderURL, "Jina reader URL"); err != nil {
		return nil, err
	}
	if configuration.CallTimeout <= 0 {
		return nil, fmt.Errorf("Jina call timeout must be positive")
	}

	return &Client{
		apiToken:   strings.TrimSpace(configuration.APIToken),
		readerURL:  strings.TrimRight(configuration.ReaderURL, "/"),
		httpClient: &http.Client{Timeout: configuration.CallTimeout},
	}, nil
}

// postJSON performs one authenticated Reader request. The target address
// travels in a JSON body rather than inside the request path so query strings
// and fragments never have to be escaped into the Reader URL.
func (client *Client) postJSON(applicationContext context.Context, requestBody any, additionalHeaders map[string]string) ([]byte, error) {
	encodedRequestBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode Jina Reader request: %w", err)
	}

	request, err := http.NewRequestWithContext(applicationContext, http.MethodPost, client.readerURL+"/", bytes.NewReader(encodedRequestBody))
	if err != nil {
		return nil, fmt.Errorf("create Jina Reader request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	// Accept-Encoding is deliberately left to the transport. Setting it here
	// would disable Go's transparent gzip decompression and leave the Jina
	// response body compressed.
	for headerName, headerValue := range additionalHeaders {
		request.Header.Set(headerName, headerValue)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		var requestURLError *url.Error
		if errors.As(err, &requestURLError) {
			err = requestURLError.Err
		}
		return nil, fmt.Errorf("call Jina Reader API: %w", err)
	}
	responseBody, readError := readLimitedResponse(response.Body)
	closeError := response.Body.Close()
	if readError != nil {
		return nil, fmt.Errorf("read Jina Reader API response: %w", readError)
	}
	if closeError != nil {
		return nil, fmt.Errorf("close Jina Reader API response: %w", closeError)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, apiResponseError("Jina Reader API", response.StatusCode, responseBody)
	}
	if !json.Valid(responseBody) {
		return nil, fmt.Errorf("Jina Reader API returned invalid JSON")
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
// memory while allowing normal Jina page payloads.
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

// apiResponseError keeps the readable Jina failure reason in tool output
// without ever including the API token. Jina reports fetch failures such as an
// unresolvable domain or a navigation timeout as HTTP 422 rather than as a
// transport error.
func apiResponseError(serviceName string, statusCode int, responseBody []byte) error {
	errorResponse := struct {
		Name            string `json:"name"`
		Message         string `json:"message"`
		ReadableMessage string `json:"readableMessage"`
	}{}
	if json.Unmarshal(responseBody, &errorResponse) == nil {
		failureReason := strings.TrimSpace(errorResponse.ReadableMessage)
		if failureReason == "" {
			failureReason = strings.TrimSpace(errorResponse.Message)
		}
		if failureReason != "" {
			return fmt.Errorf("%s returned HTTP %d: %s", serviceName, statusCode, firstLine(failureReason))
		}
		if strings.TrimSpace(errorResponse.Name) != "" {
			return fmt.Errorf("%s returned HTTP %d: %s", serviceName, statusCode, errorResponse.Name)
		}
	}
	return fmt.Errorf("%s returned HTTP %d", serviceName, statusCode)
}

// firstLine keeps a multi-line upstream diagnostic readable in a tool error by
// discarding the navigation call log Jina appends to timeout messages.
func firstLine(text string) string {
	if newlineIndex := strings.IndexByte(text, '\n'); newlineIndex >= 0 {
		return strings.TrimSpace(text[:newlineIndex])
	}
	return text
}
