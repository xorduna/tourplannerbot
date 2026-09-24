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
)

// TestAddDealNoteBuildsDocumentedRequest verifies notes are created through
// the record-specific Pipelines endpoint with Bigin field API names.
func TestAddDealNoteBuildsDocumentedRequest(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		requestCount++
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/bigin/v2/Pipelines/2034020000000489080/Notes" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Zoho-oauthtoken access-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		requestBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		expectedBody := `{"data":[{"Note_Title":"Seguiment","Note_Content":" Confirmar horaris demà. "}]}`
		if string(requestBody) != expectedBody {
			t.Errorf("body = %s, want %s", requestBody, expectedBody)
		}
		return jsonHTTPResponse(http.StatusCreated, `{"data":[{"code":"SUCCESS","details":{"id":"2034020000000723028"},"message":"record added","status":"success"}]}`), nil
	})

	addDealNoteTool, err := NewAddDealNote(newTestClient(t, transport))
	if err != nil {
		t.Fatalf("NewAddDealNote returned an error: %v", err)
	}
	encodedResult, err := addDealNoteTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080","title":" Seguiment ","content":" Confirmar horaris demà. "}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if !strings.Contains(encodedResult, `"id":"2034020000000723028"`) {
		t.Errorf("result = %s", encodedResult)
	}
	if requestCount != 1 {
		t.Errorf("request count = %d, want 1", requestCount)
	}
}

// TestAddDealNoteOmitsEmptyTitle verifies an untitled note sends only the
// mandatory note content to the record-specific endpoint.
func TestAddDealNoteOmitsEmptyTitle(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		requestBody, _ := io.ReadAll(request.Body)
		if string(requestBody) != `{"data":[{"Note_Content":"Sense títol"}]}` {
			t.Errorf("body = %s", requestBody)
		}
		return jsonHTTPResponse(http.StatusCreated, `{"data":[{"code":"SUCCESS","details":{"id":"456"},"status":"success"}]}`), nil
	})
	addDealNoteTool, _ := NewAddDealNote(newTestClient(t, transport))
	if _, err := addDealNoteTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"123","title":"","content":"Sense títol"}`)); err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
}

// TestAddDealNoteReportsMissingScope verifies Bigin's OAuth error remains
// visible after the client's one automatic access-token refresh.
func TestAddDealNoteReportsMissingScope(t *testing.T) {
	var tokenRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			requestNumber := tokenRequestCount.Add(1)
			return jsonHTTPResponse(http.StatusOK, fmt.Sprintf(`{"access_token":"token-%d","expires_in":3600}`, requestNumber)), nil
		}
		return jsonHTTPResponse(http.StatusUnauthorized, `{"code":"OAUTH_SCOPE_MISMATCH","message":"invalid oauth scope"}`), nil
	})
	addDealNoteTool, _ := NewAddDealNote(newTestClient(t, transport))
	_, err := addDealNoteTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"123","title":"","content":"Note"}`))
	if err == nil || !strings.Contains(err.Error(), "OAUTH_SCOPE_MISMATCH") {
		t.Fatalf("Execute error = %v, want OAUTH_SCOPE_MISMATCH", err)
	}
	if tokenRequestCount.Load() != 2 {
		t.Errorf("token request count = %d, want 2", tokenRequestCount.Load())
	}
}

// TestAddDealNoteRejectsInvalidArguments verifies malformed writes never reach
// Bigin.
func TestAddDealNoteRejectsInvalidArguments(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	addDealNoteTool, _ := NewAddDealNote(newTestClient(t, transport))
	invalidArguments := []string{
		`{"deal_id":"not-an-id","title":"","content":"Note"}`,
		`{"deal_id":"123","title":"","content":" "}`,
		`{"deal_id":"123","title":"","content":"Note","unexpected":true}`,
		`{"deal_id":"123","title":"","content":"Note"} {}`,
		`{"deal_id":"123","title":"` + strings.Repeat("x", maximumNoteTitleSize+1) + `","content":"Note"}`,
	}
	for _, rawArguments := range invalidArguments {
		if _, err := addDealNoteTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount)
	}
}

// TestAddDealNoteDefinitionIsStrict verifies the write tool is explicit and
// all arguments are required by the strict LLM schema.
func TestAddDealNoteDefinitionIsStrict(t *testing.T) {
	definition := (&AddDealNoteTool{}).Definition()
	if definition.Name != "add_bigin_deal_note" || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}
