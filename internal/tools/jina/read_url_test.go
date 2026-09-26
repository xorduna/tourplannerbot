package jina

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

// newTestReadURLTool creates a page reader tool whose HTTP transport is
// replaced by the supplied test function.
func newTestReadURLTool(t *testing.T, maximumContentSize int, transport roundTripFunction) *ReadURLTool {
	t.Helper()
	client, err := NewClient(Config{
		APIToken:    "api-token",
		ReaderURL:   "https://r.jina.test",
		CallTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	client.httpClient.Transport = transport

	readURLTool, err := NewReadURL(client, maximumContentSize)
	if err != nil {
		t.Fatalf("NewReadURL returned error: %v", err)
	}
	return readURLTool
}

// readURLResult mirrors the compacted JSON the tool returns to the model.
type readURLResult struct {
	URL           string     `json:"url"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	PublishedTime string     `json:"published_time"`
	HTTPStatus    int        `json:"http_status"`
	Content       string     `json:"content"`
	Truncated     bool       `json:"truncated"`
	Links         []pageLink `json:"links"`
	Warning       string     `json:"warning"`
}

// TestReadURLSendsTokenAndCompactsPage verifies the API token travels as a
// bearer header, the target address travels in the JSON body rather than the
// request path, and only the useful page fields reach the model.
func TestReadURLSendsTokenAndCompactsPage(t *testing.T) {
	var readerRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		readerRequestCount.Add(1)
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/" {
			t.Errorf("path = %q, want /", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer api-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", request.Header.Get("Accept"))
		}
		if request.Header.Get("X-With-Links-Summary") != "" {
			t.Errorf("links summary must not be requested by default")
		}
		requestBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		sentBody := struct {
			URL string `json:"url"`
		}{}
		if err := json.Unmarshal(requestBody, &sentBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if sentBody.URL != "https://example.test/hours?lang=en&utm=x" {
			t.Errorf("body url = %q, want the untouched address including its query string", sentBody.URL)
		}
		return jsonHTTPResponse(http.StatusOK, `{
			"code":200,
			"data":{
				"title":"Opening hours",
				"description":"When we open",
				"url":"https://example.test/hours?lang=en&utm=x",
				"content":"Open 9:00 to 20:00.",
				"publishedTime":"Tue, 22 Sep 2026 20:16:57 GMT",
				"httpStatus":200,
				"metadata":{"lang":"en"},
				"usage":{"tokens":1909}
			}
		}`), nil
	})

	readURLTool := newTestReadURLTool(t, 12000, transport)
	rawResult, err := readURLTool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.test/hours?lang=en&utm=x"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if readerRequestCount.Load() != 1 {
		t.Errorf("reader request count = %d, want 1", readerRequestCount.Load())
	}

	var result readURLResult
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Title != "Opening hours" || result.Content != "Open 9:00 to 20:00." {
		t.Errorf("result = %#v", result)
	}
	if result.Truncated {
		t.Error("Truncated = true, want false for a short page")
	}
	if result.Links != nil {
		t.Errorf("Links = %#v, want none when with_links is not requested", result.Links)
	}
	if strings.Contains(rawResult, "usage") || strings.Contains(rawResult, "metadata") {
		t.Errorf("result must not forward the complete Jina envelope: %s", rawResult)
	}
}

// TestReadURLReturnsBoundedLinksWhenRequested verifies with_links enables the
// Reader link summary and that the returned list is deterministic and bounded.
func TestReadURLReturnsBoundedLinksWhenRequested(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-With-Links-Summary") != "true" {
			t.Errorf("X-With-Links-Summary = %q, want true", request.Header.Get("X-With-Links-Summary"))
		}
		return jsonHTTPResponse(http.StatusOK, `{
			"code":200,
			"data":{
				"title":"Home",
				"url":"https://example.test/",
				"content":"Welcome",
				"httpStatus":200,
				"links":{
					"Tickets":"https://example.test/tickets",
					"Hours":"https://example.test/hours",
					"Empty":"   "
				}
			}
		}`), nil
	})

	readURLTool := newTestReadURLTool(t, 12000, transport)
	rawResult, err := readURLTool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.test/","with_links":true}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var result readURLResult
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Links) != 2 {
		t.Fatalf("links = %#v, want the two non-empty links", result.Links)
	}
	if result.Links[0].URL != "https://example.test/hours" || result.Links[1].URL != "https://example.test/tickets" {
		t.Errorf("links = %#v, want a deterministic order", result.Links)
	}
}

// TestReadURLTruncatesLongContent verifies an oversized page is cut on a UTF-8
// boundary and reported as truncated rather than silently shortened.
func TestReadURLTruncatesLongContent(t *testing.T) {
	longContent := strings.Repeat("à", 4000)
	transport := roundTripFunction(func(*http.Request) (*http.Response, error) {
		encodedContent, err := json.Marshal(longContent)
		if err != nil {
			t.Fatalf("encode long content: %v", err)
		}
		return jsonHTTPResponse(http.StatusOK, `{"code":200,"data":{"url":"https://example.test/","content":`+string(encodedContent)+`,"httpStatus":200}}`), nil
	})

	readURLTool := newTestReadURLTool(t, 1000, transport)
	rawResult, err := readURLTool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.test/"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var result readURLResult
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Truncated {
		t.Error("Truncated = false, want true for an oversized page")
	}
	if len(result.Content) > 1000+len("…") {
		t.Errorf("content length = %d, want at most the configured limit", len(result.Content))
	}
	if !strings.HasSuffix(result.Content, "…") {
		t.Errorf("truncated content must be marked with an ellipsis, got %q", result.Content[len(result.Content)-10:])
	}
	if !json.Valid([]byte(rawResult)) {
		t.Error("truncation produced invalid JSON, so it split a multi-byte character")
	}
}

// TestReadURLReportsEmptyPageExplicitly verifies a reachable page with no text
// is distinguishable from a failed read.
func TestReadURLReportsEmptyPageExplicitly(t *testing.T) {
	transport := roundTripFunction(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusOK, `{"code":200,"data":{"url":"https://example.test/","content":"   ","httpStatus":200}}`), nil
	})
	readURLTool := newTestReadURLTool(t, 12000, transport)

	rawResult, err := readURLTool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.test/"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var result readURLResult
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Content != "" || result.Warning == "" {
		t.Errorf("result = %#v, want empty content and an explicit warning", result)
	}
}

// TestReadURLForwardsUpstreamHTTPStatus verifies the page's own status reaches
// the model. Jina answers HTTP 200 even when the requested page was an error
// page, so the model needs the upstream status to judge the content.
func TestReadURLForwardsUpstreamHTTPStatus(t *testing.T) {
	transport := roundTripFunction(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusOK, `{"code":200,"data":{"title":"404 Not Found","url":"https://example.test/missing","content":"Page not found","httpStatus":404}}`), nil
	})
	readURLTool := newTestReadURLTool(t, 12000, transport)

	rawResult, err := readURLTool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.test/missing"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var result readURLResult
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.HTTPStatus != 404 {
		t.Errorf("HTTPStatus = %d, want the upstream 404 to reach the model", result.HTTPStatus)
	}
}

// TestReadURLRejectsInvalidArguments verifies local validation happens before
// any API quota is consumed.
func TestReadURLRejectsInvalidArguments(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		t.Errorf("unexpected Jina request for invalid arguments")
		return jsonHTTPResponse(http.StatusOK, `{}`), nil
	})
	readURLTool := newTestReadURLTool(t, 12000, transport)

	invalidArgumentCases := map[string]string{
		"empty url":     `{"url":"   "}`,
		"relative url":  `{"url":"example.test/hours"}`,
		"file scheme":   `{"url":"file:///etc/passwd"}`,
		"ftp scheme":    `{"url":"ftp://example.test/x"}`,
		"missing host":  `{"url":"https://"}`,
		"unknown field": `{"url":"https://example.test/","depth":2}`,
	}
	for caseName, rawArguments := range invalidArgumentCases {
		if _, err := readURLTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("%s: Execute returned no error", caseName)
		}
	}
}

// TestReadURLReportsFetchFailureWithoutToken verifies Jina's HTTP 422 fetch
// failures keep their readable reason, drop the navigation call log, and never
// leak the API token.
func TestReadURLReportsFetchFailureWithoutToken(t *testing.T) {
	transport := roundTripFunction(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusUnprocessableEntity, `{"data":null,"code":422,"name":"SubmittedDataMalformedError","message":"Domain 'nope.invalid' could not be resolved","readableMessage":"SubmittedDataMalformedError: Domain 'nope.invalid' could not be resolved\nCall log:\n  - navigating"}`), nil
	})
	readURLTool := newTestReadURLTool(t, 12000, transport)

	_, err := readURLTool.Execute(context.Background(), json.RawMessage(`{"url":"https://nope.invalid/"}`))
	if err == nil {
		t.Fatal("Execute returned no error for HTTP 422")
	}
	if !strings.Contains(err.Error(), "could not be resolved") {
		t.Errorf("error = %v, want the readable Jina reason", err)
	}
	if strings.Contains(err.Error(), "Call log") {
		t.Errorf("error must drop the multi-line navigation log: %v", err)
	}
	if strings.Contains(err.Error(), "api-token") {
		t.Errorf("error must not contain the API token: %v", err)
	}
}

// TestNewClientAndReadURLValidateConfiguration verifies incomplete or
// out-of-range configuration fails at startup rather than at call time.
func TestNewClientAndReadURLValidateConfiguration(t *testing.T) {
	if _, err := NewClient(Config{APIToken: " ", ReaderURL: "https://r.jina.test", CallTimeout: time.Second}); err == nil {
		t.Error("NewClient accepted an empty API token")
	}
	if _, err := NewClient(Config{APIToken: "token", ReaderURL: "r.jina.test", CallTimeout: time.Second}); err == nil {
		t.Error("NewClient accepted a relative reader URL")
	}
	if _, err := NewClient(Config{APIToken: "token", ReaderURL: "https://r.jina.test", CallTimeout: 0}); err == nil {
		t.Error("NewClient accepted a non-positive timeout")
	}

	client, err := NewClient(Config{APIToken: "token", ReaderURL: "https://r.jina.test", CallTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if _, err := NewReadURL(nil, 12000); err == nil {
		t.Error("NewReadURL accepted a missing client")
	}
	if _, err := NewReadURL(client, 0); err == nil {
		t.Error("NewReadURL accepted a zero content size")
	}
}
