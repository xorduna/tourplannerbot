// Package bigin implements native tools backed by the Zoho Bigin API.
package bigin

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

// Config contains the OAuth credentials and EU endpoint configuration used by
// the Bigin client.
type Config struct {
	RefreshToken string
	ClientID     string
	ClientSecret string
	AccountsURL  string
	APIURL       string
	CallTimeout  time.Duration
}

// Client refreshes and caches Zoho access tokens and performs Bigin API calls.
// Its mutex deliberately covers token refreshes so concurrent tool calls do not
// consume multiple access tokens unnecessarily.
type Client struct {
	refreshToken string
	clientID     string
	clientSecret string
	accountsURL  string
	apiURL       string
	httpClient   *http.Client

	tokenMutex       sync.Mutex
	accessToken      string
	accessTokenUntil time.Time
	now              func() time.Time
}

// NewClient validates configuration and creates a reusable Bigin API client.
func NewClient(configuration Config) (*Client, error) {
	if strings.TrimSpace(configuration.RefreshToken) == "" {
		return nil, fmt.Errorf("Bigin refresh token is required")
	}
	if strings.TrimSpace(configuration.ClientID) == "" {
		return nil, fmt.Errorf("Bigin client ID is required")
	}
	if strings.TrimSpace(configuration.ClientSecret) == "" {
		return nil, fmt.Errorf("Bigin client secret is required")
	}
	if err := validateBaseURL(configuration.AccountsURL, "Bigin accounts URL"); err != nil {
		return nil, err
	}
	if err := validateBaseURL(configuration.APIURL, "Bigin API URL"); err != nil {
		return nil, err
	}
	if configuration.CallTimeout <= 0 {
		return nil, fmt.Errorf("Bigin call timeout must be positive")
	}

	return &Client{
		refreshToken: strings.TrimSpace(configuration.RefreshToken),
		clientID:     strings.TrimSpace(configuration.ClientID),
		clientSecret: strings.TrimSpace(configuration.ClientSecret),
		accountsURL:  strings.TrimRight(configuration.AccountsURL, "/"),
		apiURL:       strings.TrimRight(configuration.APIURL, "/"),
		httpClient:   &http.Client{Timeout: configuration.CallTimeout},
		now:          time.Now,
	}, nil
}

// get performs one authenticated GET request. An unauthorized response
// invalidates the cached token and is retried once with a newly refreshed one.
func (client *Client) get(ctx context.Context, apiPath string) ([]byte, error) {
	for attemptNumber := 1; attemptNumber <= 2; attemptNumber++ {
		accessToken, err := client.validAccessToken(ctx)
		if err != nil {
			return nil, err
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.apiURL+apiPath, nil)
		if err != nil {
			return nil, fmt.Errorf("create Bigin request: %w", err)
		}
		request.Header.Set("Authorization", "Zoho-oauthtoken "+accessToken)
		request.Header.Set("Accept", "application/json")

		response, err := client.httpClient.Do(request)
		if err != nil {
			var requestURLError *url.Error
			if errors.As(err, &requestURLError) {
				err = requestURLError.Err
			}
			return nil, fmt.Errorf("call Bigin API: %w", err)
		}
		responseBody, readError := readLimitedResponse(response.Body)
		closeError := response.Body.Close()
		if readError != nil {
			return nil, fmt.Errorf("read Bigin API response: %w", readError)
		}
		if closeError != nil {
			return nil, fmt.Errorf("close Bigin API response: %w", closeError)
		}

		if response.StatusCode == http.StatusUnauthorized && attemptNumber == 1 {
			client.invalidateAccessToken(accessToken)
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, apiResponseError("Bigin API", response.StatusCode, responseBody)
		}
		if response.StatusCode == http.StatusNoContent {
			return []byte(`{"data":[]}`), nil
		}
		if !json.Valid(responseBody) {
			return nil, fmt.Errorf("Bigin API returned invalid JSON")
		}
		return responseBody, nil
	}
	return nil, fmt.Errorf("Bigin API authorization failed after refreshing the access token")
}

// validAccessToken returns the cached token when it has enough lifetime left,
// otherwise it refreshes it through the Zoho Accounts endpoint.
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.accountsURL+"/oauth/v2/token", strings.NewReader(formValues.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Bigin OAuth refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("refresh Bigin access token: %w", err)
	}
	responseBody, readError := readLimitedResponse(response.Body)
	closeError := response.Body.Close()
	if readError != nil {
		return "", fmt.Errorf("read Bigin OAuth response: %w", readError)
	}
	if closeError != nil {
		return "", fmt.Errorf("close Bigin OAuth response: %w", closeError)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", apiResponseError("Bigin OAuth", response.StatusCode, responseBody)
	}

	tokenResponse := struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	if err := decoder.Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("decode Bigin OAuth response: %w", err)
	}
	if tokenResponse.Error != "" {
		return "", fmt.Errorf("Bigin OAuth refresh failed: %s", tokenResponse.Error)
	}
	if strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return "", fmt.Errorf("Bigin OAuth response did not include an access token")
	}
	if tokenResponse.ExpiresIn <= 0 {
		return "", fmt.Errorf("Bigin OAuth response included an invalid token lifetime")
	}

	client.accessToken = tokenResponse.AccessToken
	client.accessTokenUntil = client.now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second)
	return client.accessToken, nil
}

// invalidateAccessToken clears a rejected token without overwriting a newer
// token that another caller may already have refreshed.
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
// memory while allowing normal Bigin record payloads.
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

// apiResponseError keeps useful Zoho error codes in tool output without ever
// including request credentials.
func apiResponseError(serviceName string, statusCode int, responseBody []byte) error {
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
			return fmt.Errorf("%s returned HTTP %d (%s): %s", serviceName, statusCode, errorCode, errorResponse.Message)
		}
		if errorCode != "" {
			return fmt.Errorf("%s returned HTTP %d: %s", serviceName, statusCode, errorCode)
		}
	}
	return fmt.Errorf("%s returned HTTP %d", serviceName, statusCode)
}
