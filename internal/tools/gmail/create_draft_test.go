package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
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

// jsonHTTPResponse creates a minimal in-memory JSON response.
func jsonHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestCreateDraftExecutesOAuthAndCreatesMIMEMessage verifies the refresh-token
// exchange, Gmail endpoint, authorization, and base64url MIME payload.
func TestCreateDraftExecutesOAuthAndCreatesMIMEMessage(t *testing.T) {
	var tokenRequestCount atomic.Int32
	var draftRequestCount atomic.Int32
	recordingTransport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/token":
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
		case "/gmail/v1/users/me/drafts":
			draftRequestCount.Add(1)
			if request.Method != http.MethodPost {
				t.Errorf("Gmail method = %s, want POST", request.Method)
			}
			if request.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			requestBody := struct {
				Message struct {
					Raw string `json:"raw"`
				} `json:"message"`
			}{}
			if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
				t.Fatalf("decode Gmail request: %v", err)
			}
			rawMessage, err := base64.RawURLEncoding.DecodeString(requestBody.Message.Raw)
			if err != nil {
				t.Fatalf("decode message.raw: %v", err)
			}
			parsedMessage, err := mail.ReadMessage(strings.NewReader(string(rawMessage)))
			if err != nil {
				t.Fatalf("parse MIME message: %v", err)
			}
			if parsedMessage.Header.Get("To") != `"Diana" <diana@example.com>` {
				t.Errorf("To = %q", parsedMessage.Header.Get("To"))
			}
			if parsedMessage.Header.Get("Cc") != "<guide@example.com>" {
				t.Errorf("Cc = %q", parsedMessage.Header.Get("Cc"))
			}
			if parsedMessage.Header.Get("Bcc") != "<archive@example.com>" {
				t.Errorf("Bcc = %q", parsedMessage.Header.Get("Bcc"))
			}
			if parsedMessage.Header.Get("Subject") != "Visita a Barcelona" {
				t.Errorf("Subject = %q", parsedMessage.Header.Get("Subject"))
			}
			messageBody, err := io.ReadAll(parsedMessage.Body)
			if err != nil {
				t.Fatalf("read message body: %v", err)
			}
			if string(messageBody) != "Hola Diana,\r\n\r\nFins aviat!" {
				t.Errorf("body = %q", messageBody)
			}
			return jsonHTTPResponse(http.StatusOK, `{"id":"draft-123","message":{"id":"message-456","threadId":"thread-789","labelIds":["DRAFT"]}}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"error":{"status":"NOT_FOUND","message":"not found"}}`), nil
		}
	})

	createDraftTool, err := NewCreateDraft(newTestClient(t, recordingTransport))
	if err != nil {
		t.Fatalf("NewCreateDraft returned an error: %v", err)
	}
	rawArguments := json.RawMessage(`{"to":["Diana <diana@example.com>"],"cc":["guide@example.com"],"bcc":["archive@example.com"],"subject":"Visita a Barcelona","body":"Hola Diana,\n\nFins aviat!"}`)
	for callNumber := 0; callNumber < 2; callNumber++ {
		encodedResult, executeError := createDraftTool.Execute(context.Background(), rawArguments)
		if executeError != nil {
			t.Fatalf("Execute returned an error: %v", executeError)
		}
		if encodedResult != `{"draft_id":"draft-123","message_id":"message-456","thread_id":"thread-789","status":"draft_created"}` {
			t.Errorf("result = %s", encodedResult)
		}
	}
	if tokenRequestCount.Load() != 1 {
		t.Errorf("token request count = %d, want 1", tokenRequestCount.Load())
	}
	if draftRequestCount.Load() != 2 {
		t.Errorf("draft request count = %d, want 2", draftRequestCount.Load())
	}
}

// TestCreateDraftRejectsInvalidArguments verifies malformed input and header
// injection attempts never reach Google.
func TestCreateDraftRejectsInvalidArguments(t *testing.T) {
	var requestCount atomic.Int32
	transport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	createDraftTool, _ := NewCreateDraft(newTestClient(t, transport))
	invalidArguments := []string{
		`{"to":[],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
		`{"to":["not-an-email"],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
		"{\"to\":[\"person@example.com\\r\\nBcc: attacker@example.com\"],\"cc\":[],\"bcc\":[],\"subject\":\"Hello\",\"body\":\"Body\"}",
		`{"to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello","body":"Body","unexpected":true}`,
		`{"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
	}
	for _, rawArguments := range invalidArguments {
		if _, err := createDraftTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount.Load() != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount.Load())
	}
}

// TestCreateDraftDefinitionIsStrict verifies the LLM receives the intended
// Gmail-specific name and source.
func TestCreateDraftDefinitionIsStrict(t *testing.T) {
	definition := (&CreateDraftTool{}).Definition()
	if definition.Name != "create_gmail_draft" || definition.Source != "gmail" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}

// TestCreateDraftRefreshesAfterUnauthorized verifies one safe retry is made
// after Gmail rejects a cached access token.
func TestCreateDraftRefreshesAfterUnauthorized(t *testing.T) {
	var tokenRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/token" {
			tokenNumber := tokenRequestCount.Add(1)
			if tokenNumber == 1 {
				return jsonHTTPResponse(http.StatusOK, `{"access_token":"rejected-token","expires_in":3600}`), nil
			}
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"accepted-token","expires_in":3600}`), nil
		}
		if request.Header.Get("Authorization") == "Bearer rejected-token" {
			return jsonHTTPResponse(http.StatusUnauthorized, `{"error":{"status":"UNAUTHENTICATED","message":"expired"}}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"id":"draft-123","message":{"id":"message-456"}}`), nil
	})
	createDraftTool, _ := NewCreateDraft(newTestClient(t, transport))
	_, err := createDraftTool.Execute(context.Background(), json.RawMessage(`{"to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if tokenRequestCount.Load() != 2 {
		t.Errorf("token request count = %d, want 2", tokenRequestCount.Load())
	}
}

// newTestClient creates a Gmail client whose traffic remains in memory.
func newTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	gmailClient, err := NewClient(Config{
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		OAuthURL:     "https://oauth.example.com/token",
		APIURL:       "https://gmail.example.com",
		CallTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient returned an error: %v", err)
	}
	gmailClient.httpClient.Transport = transport
	return gmailClient
}
