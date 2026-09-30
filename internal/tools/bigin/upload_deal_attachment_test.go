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

func TestUploadDealAttachmentUploadsQueuedFile(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("Mkdir returned an error: %v", err)
	}
	temporaryDirectory, err := os.MkdirTemp(workspaceRoot, "tourplannerbot-gmail-attachments-")
	if err != nil {
		t.Fatalf("MkdirTemp returned an error: %v", err)
	}
	attachmentPath := filepath.Join(temporaryDirectory, "contracte.pdf")
	if err := os.WriteFile(attachmentPath, []byte("PDF content"), 0o600); err != nil {
		t.Fatalf("WriteFile returned an error: %v", err)
	}

	var attachmentRequestCount atomic.Int32
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Attachments":
			attachmentRequestCount.Add(1)
			if request.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", request.Method)
			}
			if request.Header.Get("Authorization") != "Zoho-oauthtoken access-token" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			if !strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
				t.Errorf("content type = %q", request.Header.Get("Content-Type"))
			}
			file, header, err := request.FormFile("file")
			if err != nil {
				t.Errorf("FormFile returned an error: %v", err)
				return jsonHTTPResponse(http.StatusBadRequest, `{}`), nil
			}
			defer file.Close()
			content, err := io.ReadAll(file)
			if err != nil {
				t.Errorf("ReadAll returned an error: %v", err)
			}
			if header.Filename != "contracte.pdf" || string(content) != "PDF content" {
				t.Errorf("uploaded filename/content = %q/%q", header.Filename, content)
			}
			return jsonHTTPResponse(http.StatusCreated, `{"data":[{"code":"SUCCESS","details":{"id":"attachment-123"}}]}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})

	uploadTool, err := NewUploadDealAttachment(newTestClient(t, transport), workspaceRoot)
	if err != nil {
		t.Fatalf("NewUploadDealAttachment returned an error: %v", err)
	}
	executionContext, err := tools.NewExecutionContext(-100123, 7, 99)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	executionContext.RecordDownloadedFile(tools.DownloadedFile{Path: attachmentPath, Filename: "contracte.pdf", CleanupPath: temporaryDirectory})

	result, err := uploadTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"deal_id":"2034020000000489080","filename":"contracte.pdf"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if !strings.Contains(result, `"attachment-123"`) {
		t.Errorf("result = %s", result)
	}
	if attachmentRequestCount.Load() != 1 {
		t.Errorf("attachment request count = %d, want 1", attachmentRequestCount.Load())
	}
}

func TestUploadDealAttachmentRejectsUntrustedFilesWithoutCallingBigin(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("Mkdir returned an error: %v", err)
	}
	var requestCount atomic.Int32
	uploadTool, err := NewUploadDealAttachment(newTestClient(t, roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})), workspaceRoot)
	if err != nil {
		t.Fatalf("NewUploadDealAttachment returned an error: %v", err)
	}
	executionContext, _ := tools.NewExecutionContext(-100123, 7, 99)
	executionContext.RecordDownloadedFile(tools.DownloadedFile{Path: filepath.Join(workspaceRoot, "missing.pdf"), Filename: "missing.pdf"})
	trustedContext := tools.WithExecutionContext(context.Background(), executionContext)

	for _, rawArguments := range []string{
		`{"deal_id":"not-a-number","filename":"missing.pdf"}`,
		`{"deal_id":"123","filename":"../other.pdf"}`,
		`{"deal_id":"123","filename":"missing.exe"}`,
		`{"deal_id":"123","filename":"unavailable.pdf"}`,
		`{"deal_id":"123","filename":"missing.pdf","unexpected":true}`,
	} {
		if _, err := uploadTool.Execute(trustedContext, json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if _, err := uploadTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"123","filename":"missing.pdf"}`)); err == nil || !strings.Contains(err.Error(), "trusted tool execution context") {
		t.Errorf("Execute without context error = %v", err)
	}
	if requestCount.Load() != 0 {
		t.Errorf("Bigin request count = %d, want 0", requestCount.Load())
	}
}

func TestUploadDealAttachmentDefinitionIsStrict(t *testing.T) {
	definition := (&UploadDealAttachmentTool{}).Definition()
	if definition.Name != uploadDealAttachmentToolName || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if required, ok := definition.Parameters["required"].([]string); !ok || len(required) != 2 || required[0] != "deal_id" || required[1] != "filename" {
		t.Errorf("required = %#v, want deal_id and filename", definition.Parameters["required"])
	}
}
