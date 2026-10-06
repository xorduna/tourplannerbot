package bigin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tourplannerbot/internal/tools"
)

func TestDownloadDealAttachmentListsAndQueuesPDFForAnalysis(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("Mkdir returned an error: %v", err)
	}
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v2/token":
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Attachments":
			if request.Method != http.MethodGet {
				t.Errorf("list method = %s, want GET", request.Method)
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"id":"2034020000000490001","File_Name":"reserva.pdf"}]}`), nil
		case "/bigin/v2/Pipelines/2034020000000489080/Attachments/2034020000000490001":
			if request.Header.Get("Accept") != "*/*" {
				t.Errorf("download Accept = %q, want */*", request.Header.Get("Accept"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/pdf"}},
				Body:       io.NopCloser(strings.NewReader("PDF content")),
			}, nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"code":"NOT_FOUND"}`), nil
		}
	})
	downloadTool, err := NewDownloadDealAttachment(newTestClient(t, transport), workspaceRoot)
	if err != nil {
		t.Fatalf("NewDownloadDealAttachment returned an error: %v", err)
	}

	listResult, err := downloadTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080","attachment_id":null}`))
	if err != nil {
		t.Fatalf("list Execute returned an error: %v", err)
	}
	if !strings.Contains(listResult, `"File_Name":"reserva.pdf"`) {
		t.Errorf("list result = %s", listResult)
	}

	executionContext, err := tools.NewExecutionContext(-100123, 7, 99)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	downloadResult, err := downloadTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"deal_id":"2034020000000489080","attachment_id":"2034020000000490001"}`))
	if err != nil {
		t.Fatalf("download Execute returned an error: %v", err)
	}
	if !strings.Contains(downloadResult, `"status":"queued_for_analysis"`) || !strings.Contains(downloadResult, `"sent_to_telegram":false`) {
		t.Errorf("download result = %s", downloadResult)
	}
	queuedFiles := executionContext.DownloadedFiles()
	if len(queuedFiles) != 1 {
		t.Fatalf("queued files = %#v, want one", queuedFiles)
	}
	if !queuedFiles[0].AnalysisOnly || queuedFiles[0].Filename != "reserva.pdf" || queuedFiles[0].MIMEType != "application/pdf" {
		t.Errorf("queued file = %#v", queuedFiles[0])
	}
	fileContent, err := os.ReadFile(queuedFiles[0].Path)
	if err != nil || string(fileContent) != "PDF content" {
		t.Errorf("queued file content / error = %q / %v", fileContent, err)
	}
	if err := os.RemoveAll(queuedFiles[0].CleanupPath); err != nil {
		t.Fatalf("cleanup temporary directory: %v", err)
	}
}

func TestDownloadDealAttachmentRejectsInvalidArgumentsWithoutCallingBigin(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("Mkdir returned an error: %v", err)
	}
	requestCount := 0
	downloadTool, err := NewDownloadDealAttachment(newTestClient(t, roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})), workspaceRoot)
	if err != nil {
		t.Fatalf("NewDownloadDealAttachment returned an error: %v", err)
	}
	for _, rawArguments := range []string{
		`{"deal_id":"not-a-number","attachment_id":null}`,
		`{"deal_id":"123","attachment_id":"../other"}`,
		`{"deal_id":"123","attachment_id":null,"unexpected":true}`,
		`{"deal_id":"123"}`,
	} {
		if _, err := downloadTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount != 0 {
		t.Errorf("Bigin request count = %d, want 0", requestCount)
	}
}

func TestDownloadDealAttachmentDefinitionIsStrict(t *testing.T) {
	definition := (&DownloadDealAttachmentTool{}).Definition()
	if definition.Name != downloadDealAttachmentToolName || definition.Source != "bigin" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if required, ok := definition.Parameters["required"].([]string); !ok || len(required) != 2 || required[0] != "deal_id" || required[1] != "attachment_id" {
		t.Errorf("required = %#v, want deal_id and attachment_id", definition.Parameters["required"])
	}
}
