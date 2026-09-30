package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"tourplannerbot/internal/tools"
)

// TestDownloadAttachmentsQueuesPrivateTemporaryFiles verifies a named MIME
// part is fetched from Gmail, written only under a fresh temporary directory,
// and recorded for the trusted Telegram handler without exposing its path.
func TestDownloadAttachmentsQueuesPrivateTemporaryFiles(t *testing.T) {
	attachmentContent := []byte("PDF ticket content")
	encodedAttachment := base64.RawURLEncoding.EncodeToString(attachmentContent)
	attachmentRequestCount := 0
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/gmail/v1/users/me/messages/message-123":
			if request.Method != http.MethodGet || request.URL.Query().Get("format") != "full" {
				t.Errorf("message request = %s %s", request.Method, request.URL.RawQuery)
			}
			return jsonHTTPResponse(http.StatusOK, `{"id":"message-123","payload":{"mimeType":"multipart/mixed","parts":[{"filename":"../../ticket.pdf","mimeType":"application/pdf","body":{"attachmentId":"attachment-456","size":18}}]}}`), nil
		case "/gmail/v1/users/me/messages/message-123/attachments/attachment-456":
			attachmentRequestCount++
			if request.Method != http.MethodGet || request.Body != nil {
				t.Errorf("attachment request = %s, body = %v", request.Method, request.Body)
			}
			return jsonHTTPResponse(http.StatusOK, `{"size":18,"data":"`+encodedAttachment+`"}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"error":{"status":"NOT_FOUND","message":"not found"}}`), nil
		}
	})

	downloadTool, err := NewDownloadAttachments(newTestClient(t, transport))
	if err != nil {
		t.Fatalf("NewDownloadAttachments returned an error: %v", err)
	}
	executionContext, err := tools.NewExecutionContext(123, 9, 456)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	encodedResult, err := downloadTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"message_id":"message-123"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if attachmentRequestCount != 1 {
		t.Errorf("attachment requests = %d, want 1", attachmentRequestCount)
	}
	if string(encodedResult) != `{"message_id":"message-123","queued_count":1,"attachments":[{"filename":"ticket.pdf","mime_type":"application/pdf","size":18,"status":"queued_for_telegram"}]}` {
		t.Errorf("result = %s", encodedResult)
	}
	queuedFiles := executionContext.DownloadedFiles()
	if len(queuedFiles) != 1 {
		t.Fatalf("queued files = %#v, want one", queuedFiles)
	}
	if queuedFiles[0].Filename != "ticket.pdf" || queuedFiles[0].MIMEType != "application/pdf" || queuedFiles[0].Size != 18 {
		t.Errorf("queued file metadata = %#v", queuedFiles[0])
	}
	downloadedContent, err := os.ReadFile(queuedFiles[0].Path)
	if err != nil {
		t.Fatalf("read temporary file: %v", err)
	}
	if string(downloadedContent) != string(attachmentContent) {
		t.Errorf("temporary file = %q", downloadedContent)
	}
	if err := os.RemoveAll(queuedFiles[0].CleanupPath); err != nil {
		t.Fatalf("remove temporary files: %v", err)
	}
}

func TestDownloadAttachmentsRequiresTrustedContext(t *testing.T) {
	requestCount := 0
	transport := roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})
	downloadTool, _ := NewDownloadAttachments(newTestClient(t, transport))
	if _, err := downloadTool.Execute(context.Background(), json.RawMessage(`{"message_id":"message-123"}`)); err == nil {
		t.Fatal("Execute returned nil error without a trusted context")
	}
	if requestCount != 0 {
		t.Errorf("upstream request count = %d, want 0", requestCount)
	}
}

func TestDownloadAttachmentsReportsNoNamedFiles(t *testing.T) {
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"id":"message-123","payload":{"mimeType":"text/plain","body":{"data":"SGVsbG8"}}}`), nil
	})
	downloadTool, _ := NewDownloadAttachments(newTestClient(t, transport))
	executionContext, _ := tools.NewExecutionContext(123, 0, 456)
	encodedResult, err := downloadTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"message_id":"message-123"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if encodedResult != `{"message_id":"message-123","queued_count":0,"attachments":[]}` {
		t.Errorf("result = %s", encodedResult)
	}
	if len(executionContext.DownloadedFiles()) != 0 {
		t.Errorf("queued files = %#v, want none", executionContext.DownloadedFiles())
	}
}

func TestDownloadAttachmentsDefinitionIsStrict(t *testing.T) {
	definition := (&DownloadAttachmentsTool{}).Definition()
	if definition.Name != downloadGmailAttachmentsToolName || definition.Source != "gmail" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}
