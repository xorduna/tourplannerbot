package knowledgebase

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// knowledgeBaseRoundTripFunction implements http.RoundTripper for one inline
// test server without resolving the bucket's virtual-hosted URL.
type knowledgeBaseRoundTripFunction func(*http.Request) (*http.Response, error)

// RoundTrip dispatches a synthetic knowledge-base HTTP request.
func (roundTrip knowledgeBaseRoundTripFunction) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

// knowledgeBaseHTTPResponse returns a minimal object-store response.
func knowledgeBaseHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{StatusCode: statusCode, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

// newTestCatalog creates a catalog whose S3-compatible transport is supplied
// by the test instead of reaching DigitalOcean Spaces.
func newTestCatalog(t *testing.T, transport knowledgeBaseRoundTripFunction) *Catalog {
	t.Helper()
	client, err := NewClient(Config{Endpoint: "https://lon1.digitaloceanspaces.test", AccessKey: "access-key", SecretKey: "secret-key", BucketName: "dianabcntours-kb", Region: "lon1", CallTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	client.httpClient.Transport = transport
	client.now = func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }
	catalog, err := NewCatalog(client, "web/content", 5*time.Minute)
	if err != nil {
		t.Fatalf("NewCatalog returned error: %v", err)
	}
	return catalog
}

// TestGetTourCalculatesPerPersonPrice verifies structured frontmatter drives
// the price formula and that an inventory refresh is cached for its TTL.
func TestGetTourCalculatesPerPersonPrice(t *testing.T) {
	requestCount := 0
	catalog := newTestCatalog(t, func(request *http.Request) (*http.Response, error) {
		requestCount++
		if request.URL.Host != "dianabcntours-kb.lon1.digitaloceanspaces.test" {
			t.Errorf("request host = %q", request.URL.Host)
		}
		if !strings.Contains(request.Header.Get("Authorization"), "Credential=access-key/20260927/lon1/s3/aws4_request") {
			t.Errorf("unexpected Authorization header: %q", request.Header.Get("Authorization"))
		}
		if strings.Contains(request.Header.Get("Authorization"), "secret-key") {
			t.Error("Authorization header must not expose the secret key")
		}
		if request.URL.Query().Get("list-type") == "2" {
			if request.URL.Query().Get("prefix") != "web/content/" {
				t.Errorf("list prefix = %q", request.URL.Query().Get("prefix"))
			}
			return knowledgeBaseHTTPResponse(http.StatusOK, `<ListBucketResult><Contents><Key>web/content/faq.md</Key></Contents><Contents><Key>web/content/tours/sagrada-familia.md</Key></Contents></ListBucketResult>`), nil
		}
		if request.URL.Path != "/web/content/tours/sagrada-familia.md" {
			t.Errorf("object path = %q", request.URL.Path)
		}
		return knowledgeBaseHTTPResponse(http.StatusOK, "---\ntitle: Sagrada Família\ncode: SF001\nduration: 2 hours\nprice: 243\nprice_per_person: 26\naudiences: [families, seniors]\n---\n\nThe full tour source.\n"), nil
	})
	tool, err := NewGetTour(catalog)
	if err != nil {
		t.Fatalf("NewGetTour returned error: %v", err)
	}
	rawResult, err := tool.Execute(context.Background(), json.RawMessage(`{"identifier":"SF001","people":9}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	result := struct {
		TotalPrice float64 `json:"total_price_eur"`
		URL        string  `json:"url"`
		People     int     `json:"people"`
		Markdown   string  `json:"markdown"`
	}{}
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.TotalPrice != 477 || result.People != 9 {
		t.Errorf("result = %#v, want total 477 for 9 people", result)
	}
	if result.URL != "https://www.dianabarcelona.com/tours/sagrada-familia/" {
		t.Errorf("URL = %q", result.URL)
	}
	if !strings.Contains(result.Markdown, "The full tour source.") {
		t.Errorf("Markdown = %q", result.Markdown)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"identifier":"SF001"}`))
	if err != nil {
		t.Fatalf("second Execute returned error: %v", err)
	}
	if requestCount != 4 {
		t.Errorf("request count = %d, want 4 (one list, one metadata read, and two tour reads)", requestCount)
	}
}

// TestCalculateTourTotalKeepsFixedPrice verifies group size never multiplies a
// private-tour base price when the source document has no ticket supplement.
func TestCalculateTourTotalKeepsFixedPrice(t *testing.T) {
	if totalPrice := calculateTourTotal(243, nil, 9); totalPrice != 243 {
		t.Errorf("calculateTourTotal(243, nil, 9) = %v, want 243", totalPrice)
	}
}

// TestReadPageRejectsTraversal verifies a model cannot turn the page reader
// into arbitrary bucket-key access even after a valid catalog refresh.
func TestReadPageRejectsTraversal(t *testing.T) {
	catalog := newTestCatalog(t, func(request *http.Request) (*http.Response, error) {
		return knowledgeBaseHTTPResponse(http.StatusOK, `<ListBucketResult><Contents><Key>web/content/faq.md</Key></Contents></ListBucketResult>`), nil
	})
	tool, err := NewReadPage(catalog)
	if err != nil {
		t.Fatalf("NewReadPage returned error: %v", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../secrets.md"}`)); err == nil {
		t.Fatal("Execute returned nil error for traversal path")
	}
}
