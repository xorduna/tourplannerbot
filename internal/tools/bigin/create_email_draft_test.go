package bigin

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

// TestCreateEmailDraftUsesOnlyDraftEndpoint verifies the tool posts the exact
// CRM v8 draft shape and never calls a send-mail endpoint.
func TestCreateEmailDraftUsesOnlyDraftEndpoint(t *testing.T) {
	var draftRequestCount atomic.Int32
	recordingTransport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		if request.URL.Path != "/crm/v8/Deals/2034020000000489080/__email_drafts" {
			t.Errorf("unexpected API path = %s", request.URL.Path)
			return jsonHTTPResponse(http.StatusNotFound, `{"code":"NOT_FOUND"}`), nil
		}
		draftRequestCount.Add(1)
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.Header.Get("Authorization") != "Zoho-oauthtoken access-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		requestBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var payload struct {
			EmailDrafts []emailDraftPayload `json:"__email_drafts"`
		}
		if err := json.Unmarshal(requestBody, &payload); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if len(payload.EmailDrafts) != 1 {
			t.Fatalf("email drafts = %#v", payload.EmailDrafts)
		}
		draft := payload.EmailDrafts[0]
		if draft.From != "diana@example.com" || draft.Subject != "Your Barcelona visit" || draft.Content != "Hello Marta,\n\nYour plan is ready." || draft.RichText {
			t.Errorf("draft = %#v", draft)
		}
		if len(draft.To) != 1 || draft.To[0].Email != "marta@example.com" || draft.To[0].UserName != "Marta" {
			t.Errorf("recipients = %#v", draft.To)
		}
		return jsonHTTPResponse(http.StatusOK, `{"__email_drafts":[{"code":"SUCCESS","details":{"id":"draft-id"},"message":"Draft created Successfully","status":"success"}]}`), nil
	})

	biginClient := newTestClient(t, recordingTransport)
	createEmailDraftTool, err := NewCreateEmailDraft(biginClient)
	if err != nil {
		t.Fatalf("NewCreateEmailDraft returned an error: %v", err)
	}
	encodedResult, err := createEmailDraftTool.Execute(context.Background(), json.RawMessage(`{
		"deal_id":"2034020000000489080",
		"from_email":"Diana <diana@example.com>",
		"to_email":"marta@example.com",
		"to_name":"Marta",
		"subject":"Your Barcelona visit",
		"body":"Hello Marta,\n\nYour plan is ready."
	}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if !strings.Contains(encodedResult, `"id":"draft-id"`) {
		t.Errorf("result = %s", encodedResult)
	}
	if draftRequestCount.Load() != 1 {
		t.Errorf("draft request count = %d, want 1", draftRequestCount.Load())
	}
}

// TestCreateEmailDraftRejectsInvalidArguments verifies malformed addresses and
// empty content cannot reach Bigin.
func TestCreateEmailDraftRejectsInvalidArguments(t *testing.T) {
	var requestCount atomic.Int32
	countingTransport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	createEmailDraftTool, _ := NewCreateEmailDraft(newTestClient(t, countingTransport))

	invalidArguments := []string{
		`{"deal_id":"bad","from_email":"diana@example.com","to_email":"marta@example.com","to_name":"Marta","subject":"Hi","body":"Hello"}`,
		`{"deal_id":"123","from_email":"not-an-email","to_email":"marta@example.com","to_name":"Marta","subject":"Hi","body":"Hello"}`,
		`{"deal_id":"123","from_email":"diana@example.com","to_email":"marta@example.com","to_name":"Marta","subject":"Hi","body":"  "}`,
		`{"deal_id":"123","from_email":"diana@example.com","to_email":"marta@example.com","to_name":"Marta","subject":"Hi","body":"Hello","send":true}`,
	}
	for _, rawArguments := range invalidArguments {
		if _, err := createEmailDraftTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount.Load() != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount.Load())
	}
}

// TestCreateEmailDraftRejectsApplicationError verifies HTTP 200 responses with
// a failed draft item are returned as failures.
func TestCreateEmailDraftRejectsApplicationError(t *testing.T) {
	errorTransport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"__email_drafts":[{"code":"INVALID_DATA","message":"Invalid sender","status":"error"}]}`), nil
	})
	createEmailDraftTool, _ := NewCreateEmailDraft(newTestClient(t, errorTransport))
	_, err := createEmailDraftTool.Execute(context.Background(), json.RawMessage(`{
		"deal_id":"123",
		"from_email":"diana@example.com",
		"to_email":"marta@example.com",
		"to_name":"Marta",
		"subject":"Hi",
		"body":"Hello"
	}`))
	if err == nil || !strings.Contains(err.Error(), "INVALID_DATA") {
		t.Fatalf("Execute error = %v", err)
	}
}

// TestCreateEmailDraftDefinitionMakesManualSendingExplicit verifies the LLM is
// told that this operation saves only an unsent draft.
func TestCreateEmailDraftDefinitionMakesManualSendingExplicit(t *testing.T) {
	definition := (&CreateEmailDraftTool{}).Definition()
	if definition.Name != "create_bigin_email_draft" || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if !strings.Contains(strings.ToLower(definition.Description), "never sends email") {
		t.Errorf("description does not state the send boundary: %q", definition.Description)
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
