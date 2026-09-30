package bigin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGetDealEmailsListsAndReadsOneMessage(t *testing.T) {
	var emailRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Emails":
			emailRequestCount.Add(1)
			if request.Method != http.MethodGet {
				t.Errorf("Bigin method = %s, want GET", request.Method)
			}
			if request.Header.Get("Authorization") != "Zoho-oauthtoken access-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"message_id":"message-123","subject":"Consulta"}]}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Emails/message-123":
			emailRequestCount.Add(1)
			return jsonHTTPResponse(http.StatusOK, `{"email_related_list":[{"message_id":"message-123","subject":"Consulta","content":"Hola"}]}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"code":"NOT_FOUND"}`), nil
		}
	})

	emailTool, err := NewGetDealEmails(newTestClient(t, transport))
	if err != nil {
		t.Fatalf("NewGetDealEmails returned an error: %v", err)
	}

	listResult, err := emailTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080","message_id":null}`))
	if err != nil {
		t.Fatalf("list Execute returned an error: %v", err)
	}
	if !strings.Contains(listResult, `"message_id":"message-123"`) {
		t.Errorf("list result = %s", listResult)
	}

	messageResult, err := emailTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080","message_id":"message-123"}`))
	if err != nil {
		t.Fatalf("message Execute returned an error: %v", err)
	}
	if !strings.Contains(messageResult, `"content":"Hola"`) {
		t.Errorf("message result = %s", messageResult)
	}
	if emailRequestCount.Load() != 2 {
		t.Errorf("email request count = %d, want 2", emailRequestCount.Load())
	}
}

func TestGetDealEmailsRejectsInvalidArgumentsWithoutCallingBigin(t *testing.T) {
	var requestCount atomic.Int32
	emailTool, err := NewGetDealEmails(newTestClient(t, roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})))
	if err != nil {
		t.Fatalf("NewGetDealEmails returned an error: %v", err)
	}

	for _, rawArguments := range []string{
		`{"deal_id":"not-a-number","message_id":null}`,
		`{"deal_id":"123","message_id":"../../other"}`,
		`{"deal_id":"123","unexpected":true}`,
		`{"deal_id":"123"}`,
		`{}`,
	} {
		if _, err := emailTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount.Load() != 0 {
		t.Errorf("Bigin request count = %d, want 0", requestCount.Load())
	}
}

func TestGetDealEmailsDefinitionIsStrict(t *testing.T) {
	definition := (&GetDealEmailsTool{}).Definition()
	if definition.Name != getDealEmailsToolName || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if required, ok := definition.Parameters["required"].([]string); !ok || len(required) != 2 || required[0] != "deal_id" || required[1] != "message_id" {
		t.Errorf("required = %#v, want deal_id and message_id", definition.Parameters["required"])
	}
}
