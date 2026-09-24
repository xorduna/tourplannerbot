package bigin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSearchContactsBuildsDocumentedRequests verifies each lookup mode uses the
// exact Bigin Contacts endpoint and correctly URL-encodes user input.
func TestSearchContactsBuildsDocumentedRequests(t *testing.T) {
	testCases := []struct {
		name          string
		rawArguments  string
		expectedPath  string
		expectedQuery string
	}{
		{name: "ID", rawArguments: `{"search_by":"id","query":"2034020000000489022"}`, expectedPath: "/bigin/v2/Contacts/2034020000000489022"},
		{name: "word", rawArguments: `{"search_by":"word","query":"Xavier Orduña"}`, expectedPath: "/bigin/v2/Contacts/search", expectedQuery: "word=Xavier+Ordu%C3%B1a"},
		{name: "email", rawArguments: `{"search_by":"email","query":"xavier@example.com"}`, expectedPath: "/bigin/v2/Contacts/search", expectedQuery: "email=xavier%40example.com"},
		{name: "phone", rawArguments: `{"search_by":"phone","query":"+34 600 123 456"}`, expectedPath: "/bigin/v2/Contacts/search", expectedQuery: "phone=%2B34+600+123+456"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			requestCount := 0
			transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/oauth/v2/token" {
					return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
				}
				requestCount++
				if request.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", request.Method)
				}
				if request.URL.Path != testCase.expectedPath {
					t.Errorf("path = %q, want %q", request.URL.Path, testCase.expectedPath)
				}
				if request.URL.RawQuery != testCase.expectedQuery {
					t.Errorf("query = %q, want %q", request.URL.RawQuery, testCase.expectedQuery)
				}
				return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"2034020000000489022","Full_Name":"Xavier Orduña","Email":"xavier@example.com","Phone":"600123456"}]}`), nil
			})

			searchContactsTool, err := NewSearchContacts(newTestClient(t, transport))
			if err != nil {
				t.Fatalf("NewSearchContacts returned an error: %v", err)
			}
			encodedResult, err := searchContactsTool.Execute(context.Background(), json.RawMessage(testCase.rawArguments))
			if err != nil {
				t.Fatalf("Execute returned an error: %v", err)
			}
			if !strings.Contains(encodedResult, `"Email":"xavier@example.com"`) {
				t.Errorf("result = %s", encodedResult)
			}
			if requestCount != 1 {
				t.Errorf("request count = %d, want 1", requestCount)
			}
		})
	}
}

// TestSearchContactsReturnsEmptyDataForNoContent verifies Bigin's normal 204
// search response becomes useful JSON for the model.
func TestSearchContactsReturnsEmptyDataForNoContent(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		return jsonHTTPResponse(http.StatusNoContent, ""), nil
	})
	searchContactsTool, _ := NewSearchContacts(newTestClient(t, transport))
	encodedResult, err := searchContactsTool.Execute(context.Background(), json.RawMessage(`{"search_by":"word","query":"Nobody"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if encodedResult != `{"data":[]}` {
		t.Errorf("result = %s", encodedResult)
	}
}

// TestSearchContactsRejectsInvalidArguments verifies malformed lookups never
// reach Bigin.
func TestSearchContactsRejectsInvalidArguments(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	searchContactsTool, _ := NewSearchContacts(newTestClient(t, transport))
	tooLongQuery := strings.Repeat("x", maximumContactQuerySize+1)
	invalidArguments := []string{
		`{"search_by":"id","query":"not-an-id"}`,
		`{"search_by":"address","query":"Barcelona"}`,
		`{"search_by":"word","query":" "}`,
		`{"search_by":"word","query":"Xavier","unexpected":true}`,
		`{"search_by":"word","query":"` + tooLongQuery + `"}`,
	}
	for _, rawArguments := range invalidArguments {
		if _, err := searchContactsTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount)
	}
}

// TestSearchContactsDefinitionIsStrict verifies the model receives the four
// supported lookup modes through a strict schema.
func TestSearchContactsDefinitionIsStrict(t *testing.T) {
	definition := (&SearchContactsTool{}).Definition()
	if definition.Name != "search_bigin_contacts" || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}

// newTestClient creates a Bigin client whose traffic is fully contained by the
// provided in-memory transport.
func newTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
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
	biginClient.httpClient.Transport = transport
	return biginClient
}
