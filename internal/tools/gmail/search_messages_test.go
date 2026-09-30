package gmail

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// TestSearchMessagesListsAndCompactsMetadata verifies the tool converts the
// dedicated filters to one Gmail query, follows message references to metadata,
// and returns the compact result shape for a later message-read tool.
func TestSearchMessagesListsAndCompactsMetadata(t *testing.T) {
	metadataRequests := 0
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/gmail/v1/users/me/messages":
			if request.Method != http.MethodGet {
				t.Errorf("search method = %s, want GET", request.Method)
			}
			if request.Body != nil {
				t.Error("search request unexpectedly has a body")
			}
			queryValues := request.URL.Query()
			if queryValues.Get("q") != `from:"tickets@example.com" to:"diana@example.com" subject:"Your tickets" "Sagrada Familia"` {
				t.Errorf("q = %q", queryValues.Get("q"))
			}
			if queryValues.Get("maxResults") != "2" || queryValues.Get("pageToken") != "page-2" || queryValues.Get("includeSpamTrash") != "false" {
				t.Errorf("search values = %v", queryValues)
			}
			return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"message-1"},{"id":"message-2"}],"nextPageToken":"page-3","resultSizeEstimate":7}`), nil
		case "/gmail/v1/users/me/messages/message-1":
			metadataRequests++
			assertMetadataRequest(t, request)
			return jsonHTTPResponse(http.StatusOK, `{"id":"message-1","threadId":"thread-1","snippet":"  Your ticket is attached.  ","payload":{"headers":[{"name":"From","value":"Tickets <tickets@example.com>"},{"name":"To","value":"diana@example.com"},{"name":"Subject","value":"Your tickets"},{"name":"Date","value":"Tue, 1 Sep 2026 10:00:00 +0000"}]}}`), nil
		case "/gmail/v1/users/me/messages/message-2":
			metadataRequests++
			assertMetadataRequest(t, request)
			return jsonHTTPResponse(http.StatusOK, `{"id":"message-2","threadId":"thread-2","snippet":"Second ticket","payload":{"headers":[{"name":"Subject","value":"Reminder"}]}}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"error":{"status":"NOT_FOUND","message":"not found"}}`), nil
		}
	})

	searchTool, err := NewSearchMessages(newTestClient(t, transport))
	if err != nil {
		t.Fatalf("NewSearchMessages returned an error: %v", err)
	}
	encodedResult, err := searchTool.Execute(context.Background(), json.RawMessage(`{"from":"tickets@example.com","to":"diana@example.com","subject":"Your tickets","body":"Sagrada Familia","max_results":2,"page_token":"page-2"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	var result gmailMessageSearchResponse
	if err := json.Unmarshal([]byte(encodedResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Query != `from:"tickets@example.com" to:"diana@example.com" subject:"Your tickets" "Sagrada Familia"` || result.Count != 2 || result.EstimatedTotal != 7 || result.NextPageToken != "page-3" {
		t.Errorf("result summary = %#v", result)
	}
	wantMessages := []gmailMessageSearchResult{
		{MessageID: "message-1", ThreadID: "thread-1", From: "Tickets <tickets@example.com>", To: "diana@example.com", Subject: "Your tickets", Date: "Tue, 1 Sep 2026 10:00:00 +0000", Snippet: "Your ticket is attached."},
		{MessageID: "message-2", ThreadID: "thread-2", Subject: "Reminder", Snippet: "Second ticket"},
	}
	if !reflect.DeepEqual(result.Messages, wantMessages) {
		t.Errorf("messages = %#v, want %#v", result.Messages, wantMessages)
	}
	if metadataRequests != 2 {
		t.Errorf("metadata requests = %d, want 2", metadataRequests)
	}
}

func assertMetadataRequest(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Method != http.MethodGet {
		t.Errorf("metadata method = %s, want GET", request.Method)
	}
	if request.Body != nil {
		t.Error("metadata request unexpectedly has a body")
	}
	queryValues := request.URL.Query()
	if queryValues.Get("format") != "metadata" {
		t.Errorf("format = %q", queryValues.Get("format"))
	}
	wantHeaders := []string{"From", "To", "Subject", "Date"}
	if !reflect.DeepEqual(queryValues["metadataHeaders"], wantHeaders) {
		t.Errorf("metadataHeaders = %v, want %v", queryValues["metadataHeaders"], wantHeaders)
	}
}

// TestSearchMessagesRejectsInvalidArguments confirms a malformed or unbounded
// query never calls the Gmail API.
func TestSearchMessagesRejectsInvalidArguments(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	searchTool, _ := NewSearchMessages(newTestClient(t, transport))
	for _, rawArguments := range []string{
		`{}`,
		`{"body":"ticket","max_results":0}`,
		`{"body":"ticket","max_results":21}`,
		"{\"body\":\"ticket\\nnew line\"}",
		`{"body":"ticket","unexpected":true}`,
	} {
		if _, err := searchTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount)
	}
}

func TestBuildMessageSearchQueryQuotesEachFilter(t *testing.T) {
	query, err := buildMessageSearchQuery(`Alice is:unread`, "", `A "quoted" subject`, `ticket\\reference`)
	if err != nil {
		t.Fatalf("buildMessageSearchQuery returned an error: %v", err)
	}
	if want := `from:"Alice is:unread" subject:"A \"quoted\" subject" "ticket\\\\reference"`; query != want {
		t.Errorf("query = %q, want %q", query, want)
	}
}

func TestSearchMessagesDefinitionAndTruncation(t *testing.T) {
	definition := (&SearchMessagesTool{}).Definition()
	if definition.Name != searchGmailMessagesToolName || definition.Source != "gmail" || definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if got := truncateSearchText("ab€", 3); got != "ab…" {
		t.Errorf("truncateSearchText = %q, want ab…", got)
	}
}

func TestSearchPageTokenValidation(t *testing.T) {
	pageToken, err := validateSearchPageToken("  next-page  ")
	if err != nil || pageToken != "next-page" {
		t.Errorf("validateSearchPageToken = %q, %v", pageToken, err)
	}
	if _, err := validateSearchPageToken("bad\npage"); err == nil {
		t.Error("validateSearchPageToken accepted a control character")
	}
}
