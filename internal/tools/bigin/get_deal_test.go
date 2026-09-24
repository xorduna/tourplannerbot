package bigin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunction func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper for an inline test function.
func (roundTrip roundTripFunction) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

// jsonHTTPResponse creates a minimal in-memory HTTP response for client tests.
func jsonHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestGetDealExecutesOAuthAndReadOnlyLookup verifies credentials are exchanged
// through form data and the resulting token is used for the documented Bigin
// pipeline-record endpoint.
func TestGetDealExecutesOAuthAndReadOnlyLookup(t *testing.T) {
	var tokenRequestCount atomic.Int32
	var dealRequestCount atomic.Int32
	recordingTransport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			tokenRequestCount.Add(1)
			if request.Method != http.MethodPost {
				t.Errorf("OAuth method = %s, want POST", request.Method)
			}
			if err := request.ParseForm(); err != nil {
				t.Errorf("parse OAuth form: %v", err)
			}
			if request.Form.Get("refresh_token") != "refresh-token" || request.Form.Get("client_id") != "client-id" || request.Form.Get("client_secret") != "client-secret" || request.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("OAuth form = %#v", request.Form)
			}
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600,"token_type":"Bearer"}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080":
			dealRequestCount.Add(1)
			if request.Method != http.MethodGet {
				t.Errorf("Bigin method = %s, want GET", request.Method)
			}
			if request.Header.Get("Authorization") != "Zoho-oauthtoken access-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"2034020000000489080","Deal_Name":"Barcelona visit","Stage":"Qualification"}]}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"code":"NOT_FOUND"}`), nil
		}
	})

	biginClient, err := NewClient(Config{
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccountsURL:  "https://accounts.example.com",
		APIURL:       "https://api.example.com",
		CallTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient returned an error: %v", err)
	}
	biginClient.httpClient.Transport = recordingTransport
	getDealTool, err := NewGetDeal(biginClient)
	if err != nil {
		t.Fatalf("NewGetDeal returned an error: %v", err)
	}

	for callNumber := 0; callNumber < 2; callNumber++ {
		encodedResult, executeError := getDealTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080"}`))
		if executeError != nil {
			t.Fatalf("Execute returned an error: %v", executeError)
		}
		if !strings.Contains(encodedResult, `"Deal_Name":"Barcelona visit"`) {
			t.Errorf("result = %s", encodedResult)
		}
	}
	if tokenRequestCount.Load() != 1 {
		t.Errorf("token request count = %d, want 1", tokenRequestCount.Load())
	}
	if dealRequestCount.Load() != 2 {
		t.Errorf("deal request count = %d, want 2", dealRequestCount.Load())
	}
}

// TestGetDealRefreshesAfterUnauthorized verifies an access token rejected by
// Bigin is discarded and one safe retry is made with a newly refreshed token.
func TestGetDealRefreshesAfterUnauthorized(t *testing.T) {
	var tokenRequestCount atomic.Int32
	refreshingTransport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			requestNumber := tokenRequestCount.Add(1)
			return jsonHTTPResponse(http.StatusOK, fmt.Sprintf(`{"access_token":"token-%d","expires_in":3600}`, requestNumber)), nil
		}
		if request.Header.Get("Authorization") == "Zoho-oauthtoken token-1" {
			return jsonHTTPResponse(http.StatusUnauthorized, `{"code":"INVALID_TOKEN","message":"expired"}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"123"}]}`), nil
	})

	biginClient, err := NewClient(Config{
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccountsURL:  "https://accounts.example.com",
		APIURL:       "https://api.example.com",
		CallTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient returned an error: %v", err)
	}
	biginClient.httpClient.Transport = refreshingTransport
	getDealTool, _ := NewGetDeal(biginClient)
	if _, err := getDealTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"123"}`)); err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if tokenRequestCount.Load() != 2 {
		t.Errorf("token request count = %d, want 2", tokenRequestCount.Load())
	}
}

// TestGetDealRejectsInvalidArguments verifies IDs remain opaque numeric strings
// and unexpected model arguments never reach the upstream service.
func TestGetDealRejectsInvalidArguments(t *testing.T) {
	biginClient, err := NewClient(Config{
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AccountsURL:  "https://accounts.example.com",
		APIURL:       "https://api.example.com",
		CallTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient returned an error: %v", err)
	}
	getDealTool, _ := NewGetDeal(biginClient)

	for _, rawArguments := range []string{
		`{"deal_id":"not-a-number"}`,
		`{"deal_id":"123","unexpected":true}`,
		`{}`,
	} {
		if _, err := getDealTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
}

// TestGetDealDefinitionIsStrict verifies the LLM schema requires only the deal
// ID and identifies Bigin as the source used by Telegram progress reporting.
func TestGetDealDefinitionIsStrict(t *testing.T) {
	getDealTool := &GetDealTool{}
	definition := getDealTool.Definition()
	if definition.Name != "get_bigin_deal" || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}
