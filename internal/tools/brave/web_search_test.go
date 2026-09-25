package brave

import (
	"context"
	"encoding/json"
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

// newTestWebSearchTool creates a web search tool whose HTTP transport is
// replaced by the supplied test function.
func newTestWebSearchTool(t *testing.T, defaultResultCount int, transport roundTripFunction) *WebSearchTool {
	t.Helper()
	client, err := NewClient(Config{
		SubscriptionToken: "subscription-token",
		APIURL:            "https://api.search.brave.test",
		CallTimeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	client.httpClient.Transport = transport

	webSearchTool, err := NewWebSearch(client, defaultResultCount)
	if err != nil {
		t.Fatalf("NewWebSearch returned error: %v", err)
	}
	return webSearchTool
}

// TestWebSearchSendsTokenAndCompactsResults verifies the subscription token is
// sent as a header, the default result count is applied, and only the compacted
// fields reach the model.
func TestWebSearchSendsTokenAndCompactsResults(t *testing.T) {
	var searchRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		searchRequestCount.Add(1)
		if request.URL.Path != "/res/v1/web/search" {
			t.Errorf("path = %q, want /res/v1/web/search", request.URL.Path)
		}
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.Header.Get("X-Subscription-Token") != "subscription-token" {
			t.Errorf("X-Subscription-Token = %q", request.Header.Get("X-Subscription-Token"))
		}
		if request.URL.Query().Get("q") != "sagrada familia horaris" {
			t.Errorf("q = %q", request.URL.Query().Get("q"))
		}
		if request.URL.Query().Get("count") != "5" {
			t.Errorf("count = %q, want 5", request.URL.Query().Get("count"))
		}
		if request.URL.Query().Has("freshness") {
			t.Errorf("freshness must be absent when it is not requested")
		}
		return jsonHTTPResponse(http.StatusOK, `{
			"query":{"original":"sagrada familia horaris"},
			"web":{"results":[
				{"title":"Sagrada Fam&iacute;lia","url":"https://sagradafamilia.org","description":"Obert de <strong>9:00</strong>  a 18:00","age":"2 days ago","profile":{"name":"ignored"}},
				{"title":"Horaris","url":"https://example.test/horaris","description":"Horaris oficials","page_age":"2026-01-02T00:00:00"}
			]}
		}`), nil
	})

	webSearchTool := newTestWebSearchTool(t, 5, transport)
	rawResult, err := webSearchTool.Execute(context.Background(), json.RawMessage(`{"query":"sagrada familia horaris"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if searchRequestCount.Load() != 1 {
		t.Errorf("search request count = %d, want 1", searchRequestCount.Load())
	}

	result := struct {
		Query   string            `json:"query"`
		Count   int               `json:"count"`
		Results []webSearchResult `json:"results"`
	}{}
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Query != "sagrada familia horaris" {
		t.Errorf("query = %q", result.Query)
	}
	if result.Count != 2 || len(result.Results) != 2 {
		t.Fatalf("count = %d, results = %d, want 2 and 2", result.Count, len(result.Results))
	}
	if result.Results[0].Title != "Sagrada Família" {
		t.Errorf("title = %q, want decoded HTML entity", result.Results[0].Title)
	}
	if result.Results[0].Description != "Obert de 9:00 a 18:00" {
		t.Errorf("description = %q, want markup removed and whitespace collapsed", result.Results[0].Description)
	}
	if result.Results[0].Age != "2 days ago" {
		t.Errorf("age = %q", result.Results[0].Age)
	}
	if result.Results[1].Age != "2026-01-02T00:00:00" {
		t.Errorf("second age = %q, want the page age fallback", result.Results[1].Age)
	}
	if strings.Contains(rawResult, "profile") {
		t.Errorf("result must not forward the complete Brave envelope: %s", rawResult)
	}
}

// TestWebSearchAppliesRequestedCountAndFreshness verifies explicit arguments
// are forwarded and the returned list is limited to the requested size.
func TestWebSearchAppliesRequestedCountAndFreshness(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("count") != "1" {
			t.Errorf("count = %q, want 1", request.URL.Query().Get("count"))
		}
		if request.URL.Query().Get("freshness") != "pw" {
			t.Errorf("freshness = %q, want pw", request.URL.Query().Get("freshness"))
		}
		return jsonHTTPResponse(http.StatusOK, `{
			"query":{"original":"concerts barcelona"},
			"web":{"results":[
				{"title":"One","url":"https://example.test/one","description":"First"},
				{"title":"Two","url":"https://example.test/two","description":"Second"}
			]}
		}`), nil
	})

	webSearchTool := newTestWebSearchTool(t, 5, transport)
	rawResult, err := webSearchTool.Execute(context.Background(), json.RawMessage(`{"query":"concerts barcelona","count":1,"freshness":"pw"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	result := struct {
		Results []webSearchResult `json:"results"`
	}{}
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].URL != "https://example.test/one" {
		t.Errorf("results = %#v, want only the first hit", result.Results)
	}
}

// TestWebSearchRejectsInvalidArguments verifies local validation happens before
// any API quota is consumed.
func TestWebSearchRejectsInvalidArguments(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		t.Errorf("unexpected Brave request for invalid arguments: %s", request.URL)
		return jsonHTTPResponse(http.StatusOK, `{}`), nil
	})
	webSearchTool := newTestWebSearchTool(t, 5, transport)

	invalidArgumentCases := map[string]string{
		"empty query":       `{"query":"   "}`,
		"count too large":   `{"query":"barcelona","count":50}`,
		"count too small":   `{"query":"barcelona","count":0}`,
		"unknown freshness": `{"query":"barcelona","freshness":"ph"}`,
		"unknown field":     `{"query":"barcelona","country":"ES"}`,
		"query too long":    `{"query":"` + strings.Repeat("a", maximumQueryLength+1) + `"}`,
	}
	for caseName, rawArguments := range invalidArgumentCases {
		if _, err := webSearchTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("%s: Execute returned no error", caseName)
		}
	}
}

// TestWebSearchReportsAPIErrorWithoutToken verifies upstream failures keep the
// Brave error identifier and never leak the subscription token.
func TestWebSearchReportsAPIErrorWithoutToken(t *testing.T) {
	transport := roundTripFunction(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusTooManyRequests, `{"type":"ErrorResponse","error":{"id":"abc","code":"RATE_LIMITED","detail":"Too many requests"}}`), nil
	})
	webSearchTool := newTestWebSearchTool(t, 5, transport)

	_, err := webSearchTool.Execute(context.Background(), json.RawMessage(`{"query":"barcelona"}`))
	if err == nil {
		t.Fatal("Execute returned no error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "RATE_LIMITED") {
		t.Errorf("error = %v, want the Brave error code", err)
	}
	if strings.Contains(err.Error(), "subscription-token") {
		t.Errorf("error must not contain the subscription token: %v", err)
	}
}

// TestNewClientAndWebSearchValidateConfiguration verifies incomplete or
// out-of-range configuration fails at startup rather than at call time.
func TestNewClientAndWebSearchValidateConfiguration(t *testing.T) {
	if _, err := NewClient(Config{SubscriptionToken: " ", APIURL: "https://api.search.brave.test", CallTimeout: time.Second}); err == nil {
		t.Error("NewClient accepted an empty subscription token")
	}
	if _, err := NewClient(Config{SubscriptionToken: "token", APIURL: "api.search.brave.test", CallTimeout: time.Second}); err == nil {
		t.Error("NewClient accepted a relative API URL")
	}
	if _, err := NewClient(Config{SubscriptionToken: "token", APIURL: "https://api.search.brave.test", CallTimeout: 0}); err == nil {
		t.Error("NewClient accepted a non-positive timeout")
	}

	client, err := NewClient(Config{SubscriptionToken: "token", APIURL: "https://api.search.brave.test", CallTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if _, err := NewWebSearch(nil, 5); err == nil {
		t.Error("NewWebSearch accepted a missing client")
	}
	if _, err := NewWebSearch(client, 0); err == nil {
		t.Error("NewWebSearch accepted a zero default result count")
	}
	if _, err := NewWebSearch(client, maximumResultCount+1); err == nil {
		t.Error("NewWebSearch accepted a default result count above the Brave maximum")
	}
}
