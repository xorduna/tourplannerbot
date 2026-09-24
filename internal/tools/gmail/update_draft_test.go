package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"testing"
)

// TestUpdateDraftReplacesCompleteMessage verifies the tool uses Gmail's PUT
// endpoint and sends the full replacement MIME message.
func TestUpdateDraftReplacesCompleteMessage(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		requestCount++
		if request.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", request.Method)
		}
		if request.URL.Path != "/gmail/v1/users/me/drafts/r-123_ABC" {
			t.Errorf("path = %q", request.URL.Path)
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
		if parsedMessage.Header.Get("To") != "<new@example.com>" {
			t.Errorf("To = %q", parsedMessage.Header.Get("To"))
		}
		if parsedMessage.Header.Get("Subject") != "Updated subject" {
			t.Errorf("Subject = %q", parsedMessage.Header.Get("Subject"))
		}
		messageBody, err := io.ReadAll(parsedMessage.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(messageBody) != "Updated body" {
			t.Errorf("body = %q", messageBody)
		}
		return jsonHTTPResponse(http.StatusOK, `{"id":"r-123_ABC","message":{"id":"new-message-456","threadId":"thread-789"}}`), nil
	})

	updateDraftTool, err := NewUpdateDraft(newTestClient(t, transport))
	if err != nil {
		t.Fatalf("NewUpdateDraft returned an error: %v", err)
	}
	encodedResult, err := updateDraftTool.Execute(context.Background(), json.RawMessage(`{"draft_id":"r-123_ABC","to":["new@example.com"],"cc":[],"bcc":[],"subject":"Updated subject","body":"Updated body"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if encodedResult != `{"draft_id":"r-123_ABC","message_id":"new-message-456","thread_id":"thread-789","status":"draft_updated"}` {
		t.Errorf("result = %s", encodedResult)
	}
	if requestCount != 1 {
		t.Errorf("request count = %d, want 1", requestCount)
	}
}

// TestUpdateDraftRejectsInvalidArguments verifies unsafe draft IDs and invalid
// replacement messages never reach Google.
func TestUpdateDraftRejectsInvalidArguments(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	updateDraftTool, _ := NewUpdateDraft(newTestClient(t, transport))
	invalidArguments := []string{
		`{"draft_id":"","to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
		`{"draft_id":"../another-resource","to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
		`{"draft_id":"r-123","to":[],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
		`{"draft_id":"r-123","to":["invalid"],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`,
		`{"draft_id":"r-123","to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello\nBcc: attacker@example.com","body":"Body"}`,
		`{"draft_id":"r-123","to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello","body":"Body","unexpected":true}`,
	}
	for _, rawArguments := range invalidArguments {
		if _, err := updateDraftTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount)
	}
}

// TestUpdateDraftRejectsMismatchedResponseID ensures an unexpected Gmail
// response cannot be reported as a successful update of another draft.
func TestUpdateDraftRejectsMismatchedResponseID(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"id":"another-draft","message":{"id":"message-456"}}`), nil
	})
	updateDraftTool, _ := NewUpdateDraft(newTestClient(t, transport))
	_, err := updateDraftTool.Execute(context.Background(), json.RawMessage(`{"draft_id":"r-123","to":["person@example.com"],"cc":[],"bcc":[],"subject":"Hello","body":"Body"}`))
	if err == nil {
		t.Fatal("Execute returned nil error for a mismatched Gmail draft ID")
	}
}

// TestUpdateDraftDefinitionIsStrict verifies the LLM-facing name, source, and
// strict full-replacement contract.
func TestUpdateDraftDefinitionIsStrict(t *testing.T) {
	definition := (&UpdateDraftTool{}).Definition()
	if definition.Name != "update_gmail_draft" || definition.Source != "gmail" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}
