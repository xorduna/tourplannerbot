// Package gmail implements native tools backed by the Gmail API.
package gmail

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
	"sync"
	"time"
)

const (
	maximumResponseBytes = 4 << 20
	tokenExpiryMargin    = time.Minute
)

// Config contains the OAuth credentials and endpoints used by the Gmail
// client. Endpoint overrides keep the client fully testable without external
// network access.
type Config struct {
	RefreshToken string
	ClientID     string
	ClientSecret string
	OAuthURL     string
	APIURL       string
	CallTimeout  time.Duration
}

// Client refreshes and caches Google access tokens and performs Gmail API
// calls. The token mutex prevents concurrent calls from refreshing the same
// access token more than once.
type Client struct {
	refreshToken string
	clientID     string
	clientSecret string
	oauthURL     string
	apiURL       string
	httpClient   *http.Client

	tokenMutex       sync.Mutex
	accessToken      string
	accessTokenUntil time.Time
	now              func() time.Time
}

// NewClient validates configuration and creates a reusable Gmail API client.
func NewClient(configuration Config) (*Client, error) {
	if strings.TrimSpace(configuration.RefreshToken) == "" {
		return nil, fmt.Errorf("Gmail refresh token is required")
	}
	if strings.TrimSpace(configuration.ClientID) == "" {
		return nil, fmt.Errorf("Gmail client ID is required")
	}
	if strings.TrimSpace(configuration.ClientSecret) == "" {
		return nil, fmt.Errorf("Gmail client secret is required")
	}
	if err := validateBaseURL(configuration.OAuthURL, "Gmail OAuth URL"); err != nil {
		return nil, err
	}
	if err := validateBaseURL(configuration.APIURL, "Gmail API URL"); err != nil {
		return nil, err
	}
	if configuration.CallTimeout <= 0 {
		return nil, fmt.Errorf("Gmail call timeout must be positive")
	}

	return &Client{
		refreshToken: strings.TrimSpace(configuration.RefreshToken),
		clientID:     strings.TrimSpace(configuration.ClientID),
		clientSecret: strings.TrimSpace(configuration.ClientSecret),
		oauthURL:     strings.TrimRight(strings.TrimSpace(configuration.OAuthURL), "/"),
		apiURL:       strings.TrimRight(strings.TrimSpace(configuration.APIURL), "/"),
		httpClient:   &http.Client{Timeout: configuration.CallTimeout},
		now:          time.Now,
	}, nil
}

// doJSON performs one authenticated JSON request. An unauthorized response
// invalidates the cached token and is retried once with a fresh token.
func (client *Client) doJSON(ctx context.Context, method string, apiPath string, requestBody any) ([]byte, error) {
	encodedRequestBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode Gmail API request: %w", err)
	}

	for attemptNumber := 1; attemptNumber <= 2; attemptNumber++ {
		accessToken, err := client.validAccessToken(ctx)
		if err != nil {
			return nil, err
		}

		request, err := http.NewRequestWithContext(ctx, method, client.apiURL+apiPath, bytes.NewReader(encodedRequestBody))
		if err != nil {
			return nil, fmt.Errorf("create Gmail API request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")

		response, err := client.httpClient.Do(request)
		if err != nil {
			var requestURLError *url.Error
			if errors.As(err, &requestURLError) {
				err = requestURLError.Err
			}
			return nil, fmt.Errorf("call Gmail API: %w", err)
		}
		responseBody, readError := readLimitedResponse(response.Body)
		closeError := response.Body.Close()
		if readError != nil {
			return nil, fmt.Errorf("read Gmail API response: %w", readError)
		}
		if closeError != nil {
			return nil, fmt.Errorf("close Gmail API response: %w", closeError)
		}

		if response.StatusCode == http.StatusUnauthorized && attemptNumber == 1 {
			client.invalidateAccessToken(accessToken)
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, googleAPIResponseError("Gmail API", response.StatusCode, responseBody)
		}
		if !json.Valid(responseBody) {
			return nil, fmt.Errorf("Gmail API returned invalid JSON")
		}
		return responseBody, nil
	}
	return nil, fmt.Errorf("Gmail API authorization failed after refreshing the access token")
}

// validAccessToken returns a cached token with sufficient remaining lifetime,
// or refreshes it through Google's OAuth token endpoint.
func (client *Client) validAccessToken(ctx context.Context) (string, error) {
	client.tokenMutex.Lock()
	defer client.tokenMutex.Unlock()

	if client.accessToken != "" && client.now().Add(tokenExpiryMargin).Before(client.accessTokenUntil) {
		return client.accessToken, nil
	}

	formValues := url.Values{
		"refresh_token": {client.refreshToken},
		"client_id":     {client.clientID},
		"client_secret": {client.clientSecret},
		"grant_type":    {"refresh_token"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.oauthURL, strings.NewReader(formValues.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Gmail OAuth refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("refresh Gmail access token: %w", err)
	}
	responseBody, readError := readLimitedResponse(response.Body)
	closeError := response.Body.Close()
	if readError != nil {
		return "", fmt.Errorf("read Gmail OAuth response: %w", readError)
	}
	if closeError != nil {
		return "", fmt.Errorf("close Gmail OAuth response: %w", closeError)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", googleAPIResponseError("Gmail OAuth", response.StatusCode, responseBody)
	}

	tokenResponse := struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	if err := decoder.Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("decode Gmail OAuth response: %w", err)
	}
	if tokenResponse.Error != "" {
		return "", fmt.Errorf("Gmail OAuth refresh failed: %s", tokenResponse.Error)
	}
	if strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return "", fmt.Errorf("Gmail OAuth response did not include an access token")
	}
	if tokenResponse.ExpiresIn <= 0 {
		return "", fmt.Errorf("Gmail OAuth response included an invalid token lifetime")
	}

	client.accessToken = tokenResponse.AccessToken
	client.accessTokenUntil = client.now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second)
	return client.accessToken, nil
}

// invalidateAccessToken clears a rejected token without overwriting a newer
// token another caller may already have refreshed.
func (client *Client) invalidateAccessToken(rejectedAccessToken string) {
	client.tokenMutex.Lock()
	defer client.tokenMutex.Unlock()
	if client.accessToken == rejectedAccessToken {
		client.accessToken = ""
		client.accessTokenUntil = time.Time{}
	}
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
// memory while allowing normal Gmail resource payloads.
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

// googleAPIResponseError extracts Google's structured status without exposing
// request credentials or returning an unbounded upstream payload.
func googleAPIResponseError(serviceName string, statusCode int, responseBody []byte) error {
	errorResponse := struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}{}
	if json.Unmarshal(responseBody, &errorResponse) == nil {
		errorStatus := strings.TrimSpace(errorResponse.Error.Status)
		errorMessage := strings.TrimSpace(errorResponse.Error.Message)
		if errorStatus != "" && errorMessage != "" {
			return fmt.Errorf("%s returned HTTP %d (%s): %s", serviceName, statusCode, errorStatus, errorMessage)
		}
		if errorMessage != "" {
			return fmt.Errorf("%s returned HTTP %d: %s", serviceName, statusCode, errorMessage)
		}
	}
	return fmt.Errorf("%s returned HTTP %d", serviceName, statusCode)
}
