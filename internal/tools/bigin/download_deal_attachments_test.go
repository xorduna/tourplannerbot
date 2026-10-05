package bigin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"tourplannerbot/internal/tools"
)

// binaryHTTPResponse creates an in-memory HTTP response carrying raw bytes for
// attachment download tests.
func binaryHTTPResponse(statusCode int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}

func TestDownloadDealAttachmentsListsAttachments(t *testing.T) {
	workspaceRoot := t.TempDir()
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Attachments":
			if request.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", request.Method)
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"777","File_Name":"contracte.pdf","$size":"11"},{"id":"888","File_Name":"../evil.pdf","Size":22}]}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})

	downloadTool, err := NewDownloadDealAttachments(newTestClient(t, transport), workspaceRoot)
	if err != nil {
		t.Fatalf("NewDownloadDealAttachments returned an error: %v", err)
	}
	result, err := downloadTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080","attachment_id":null}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}

	parsed := struct {
		Count       int `json:"count"`
		Attachments []struct {
			ID       string `json:"id"`
			FileName string `json:"file_name"`
			Size     int64  `json:"size"`
		} `json:"attachments"`
	}{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Unmarshal result returned an error: %v (result=%s)", err, result)
	}
	if parsed.Count != 2 {
		t.Fatalf("count = %d, want 2 (result=%s)", parsed.Count, result)
	}
	if parsed.Attachments[0].FileName != "contracte.pdf" || parsed.Attachments[0].Size != 11 {
		t.Errorf("first attachment = %#v", parsed.Attachments[0])
	}
	if parsed.Attachments[1].FileName != "evil.pdf" || parsed.Attachments[1].Size != 22 {
		t.Errorf("second attachment should be sanitized: %#v", parsed.Attachments[1])
	}
}

func TestDownloadDealAttachmentsDownloadsAndQueuesFile(t *testing.T) {
	workspaceRoot := t.TempDir()
	fileContent := []byte("PDF content")
	var downloadRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Attachments":
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"777","File_Name":"contracte.pdf","$size":"11"}]}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Attachments/777":
			downloadRequestCount.Add(1)
			if request.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", request.Method)
			}
			if request.Header.Get("Authorization") != "Zoho-oauthtoken access-token" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			return binaryHTTPResponse(http.StatusOK, fileContent), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})

	downloadTool, err := NewDownloadDealAttachments(newTestClient(t, transport), workspaceRoot)
	if err != nil {
		t.Fatalf("NewDownloadDealAttachments returned an error: %v", err)
	}
	executionContext, err := tools.NewExecutionContext(-100123, 7, 99)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}

	result, err := downloadTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"deal_id":"2034020000000489080","attachment_id":"777"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if downloadRequestCount.Load() != 1 {
		t.Errorf("download request count = %d, want 1", downloadRequestCount.Load())
	}
	if !strings.Contains(result, `"queued_for_telegram"`) || !strings.Contains(result, `"contracte.pdf"`) {
		t.Errorf("result = %s", result)
	}

	queuedFiles := executionContext.DownloadedFiles()
	if len(queuedFiles) != 1 {
		t.Fatalf("queued files = %d, want 1", len(queuedFiles))
	}
	if queuedFiles[0].Filename != "contracte.pdf" {
		t.Errorf("queued filename = %q", queuedFiles[0].Filename)
	}
	if !pathWithin(workspaceRoot, queuedFiles[0].Path) {
		t.Errorf("queued path %q is outside the workspace %q", queuedFiles[0].Path, workspaceRoot)
	}
	writtenContent, err := os.ReadFile(queuedFiles[0].Path)
	if err != nil {
		t.Fatalf("ReadFile returned an error: %v", err)
	}
	if string(writtenContent) != string(fileContent) {
		t.Errorf("written content = %q, want %q", writtenContent, fileContent)
	}
}

func TestDownloadDealAttachmentsRejectsInvalidRequests(t *testing.T) {
	workspaceRoot := t.TempDir()
	var requestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/123/Attachments":
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"777","File_Name":"contracte.pdf","$size":"11"}]}`), nil
		default:
			requestCount.Add(1)
			return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
		}
	})

	downloadTool, err := NewDownloadDealAttachments(newTestClient(t, transport), workspaceRoot)
	if err != nil {
		t.Fatalf("NewDownloadDealAttachments returned an error: %v", err)
	}
	executionContext, _ := tools.NewExecutionContext(-100123, 7, 99)
	trustedContext := tools.WithExecutionContext(context.Background(), executionContext)

	for _, rawArguments := range []string{
		`{"deal_id":"not-a-number","attachment_id":null}`,
		`{"deal_id":"123","attachment_id":"not-a-number"}`,
		`{"deal_id":"123","attachment_id":"999"}`,
		`{"deal_id":"123","attachment_id":"777","unexpected":true}`,
	} {
		if _, err := downloadTool.Execute(trustedContext, json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	// A download without a trusted execution context must be refused.
	if _, err := downloadTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"123","attachment_id":"777"}`)); err == nil || !strings.Contains(err.Error(), "trusted tool execution context") {
		t.Errorf("Execute without context error = %v", err)
	}
	// No attachment download endpoint should have been reached for any rejection.
	if requestCount.Load() != 0 {
		t.Errorf("unexpected Bigin request count = %d, want 0", requestCount.Load())
	}
}

func TestDownloadDealAttachmentsDefinitionIsStrict(t *testing.T) {
	definition := (&DownloadDealAttachmentsTool{}).Definition()
	if definition.Name != downloadDealAttachmentsToolName || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if required, ok := definition.Parameters["required"].([]string); !ok || len(required) != 2 || required[0] != "deal_id" || required[1] != "attachment_id" {
		t.Errorf("required = %#v, want deal_id and attachment_id", definition.Parameters["required"])
	}
}

func TestDownloadDealAttachmentsCleansTemporaryDirectoryOnFailure(t *testing.T) {
	workspaceRoot := t.TempDir()
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/123/Attachments":
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"777","File_Name":"contracte.pdf","$size":"11"}]}`), nil
		case "/bigin/v2/Pipelines/123/Attachments/777":
			return binaryHTTPResponse(http.StatusOK, nil), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})

	downloadTool, err := NewDownloadDealAttachments(newTestClient(t, transport), workspaceRoot)
	if err != nil {
		t.Fatalf("NewDownloadDealAttachments returned an error: %v", err)
	}
	executionContext, _ := tools.NewExecutionContext(-100123, 7, 99)
	if _, err := downloadTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"deal_id":"123","attachment_id":"777"}`)); err == nil {
		t.Fatalf("Execute returned nil error for an empty download")
	}
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir returned an error: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tourplannerbot-bigin-attachments-") {
			t.Errorf("temporary directory %q was left behind in %q", entry.Name(), filepath.Clean(workspaceRoot))
		}
	}
}
