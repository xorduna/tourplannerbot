package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tourplannerbot/internal/llm"
	applicationTools "tourplannerbot/internal/tools"

	"github.com/go-telegram/bot"
)

// TestSendDownloadedFilesUploadsDocument verifies a file queued by a trusted
// native tool is posted to the original chat and forum topic as a document.
func TestSendDownloadedFilesUploadsDocument(t *testing.T) {
	temporaryDirectory := t.TempDir()
	temporaryFile := filepath.Join(temporaryDirectory, "attachment")
	if err := os.WriteFile(temporaryFile, []byte("ticket PDF"), 0o600); err != nil {
		t.Fatalf("write temporary file: %v", err)
	}
	requestCount := 0
	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		if !strings.HasSuffix(request.URL.Path, "/sendDocument") {
			t.Errorf("path = %q", request.URL.Path)
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if request.FormValue("chat_id") != "-100123" || request.FormValue("message_thread_id") != "77" {
			t.Errorf("chat routing = chat_id:%q topic:%q", request.FormValue("chat_id"), request.FormValue("message_thread_id"))
		}
		document, documentHeader, err := request.FormFile("document")
		if err != nil {
			t.Fatalf("open document form part: %v", err)
		}
		defer document.Close()
		documentContent, err := io.ReadAll(document)
		if err != nil {
			t.Fatalf("read document form part: %v", err)
		}
		if documentHeader.Filename != "tickets.pdf" || string(documentContent) != "ticket PDF" {
			t.Errorf("document = %q / %q", documentHeader.Filename, documentContent)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer testServer.Close()
	telegramBot, err := bot.New("test-token", bot.WithSkipGetMe(), bot.WithServerURL(testServer.URL))
	if err != nil {
		t.Fatalf("create Telegram bot: %v", err)
	}
	telegramHandler := &Handler{logger: slog.Default()}
	telegramHandler.sendDownloadedFiles(context.Background(), telegramBot, -100123, 77, []applicationTools.DownloadedFile{{
		Path:        temporaryFile,
		Filename:    "tickets.pdf",
		MIMEType:    "application/pdf",
		Size:        10,
		CleanupPath: temporaryDirectory,
	}})
	if requestCount != 1 {
		t.Errorf("sendDocument requests = %d, want 1", requestCount)
	}
}

func TestSendDownloadedFilesSkipsAnalysisOnlyFiles(t *testing.T) {
	temporaryFile := filepath.Join(t.TempDir(), "attachment.pdf")
	if err := os.WriteFile(temporaryFile, []byte("private source"), 0o600); err != nil {
		t.Fatalf("write temporary file: %v", err)
	}
	requestCount := 0
	testServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		requestCount++
	}))
	defer testServer.Close()
	telegramBot, err := bot.New("test-token", bot.WithSkipGetMe(), bot.WithServerURL(testServer.URL))
	if err != nil {
		t.Fatalf("create Telegram bot: %v", err)
	}
	telegramHandler := &Handler{logger: slog.Default()}
	telegramHandler.sendDownloadedFiles(context.Background(), telegramBot, -100123, 77, []applicationTools.DownloadedFile{{
		Path: temporaryFile, Filename: "attachment.pdf", AnalysisOnly: true,
	}})
	if requestCount != 0 {
		t.Errorf("Telegram request count = %d, want 0", requestCount)
	}
}

func TestCleanupDownloadedFilesRemovesOnlyQueuedTemporaryDirectories(t *testing.T) {
	workspaceDirectory := filepath.Join(t.TempDir(), "tmp")
	if err := os.MkdirAll(workspaceDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	temporaryDirectory, err := os.MkdirTemp(workspaceDirectory, "tourplannerbot-gmail-attachments-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	temporaryFile := filepath.Join(temporaryDirectory, "attachment")
	if err := os.WriteFile(temporaryFile, []byte("ticket"), 0o600); err != nil {
		t.Fatalf("write temporary file: %v", err)
	}
	cleanupDownloadedFiles([]applicationTools.DownloadedFile{{CleanupPath: temporaryDirectory}})
	if _, err := os.Stat(temporaryDirectory); !os.IsNotExist(err) {
		t.Errorf("temporary directory still exists or stat failed: %v", err)
	}
}

func TestCleanupDownloadedFilesRejectsUnmanagedDirectory(t *testing.T) {
	unmanagedDirectory := t.TempDir()
	cleanupDownloadedFiles([]applicationTools.DownloadedFile{{CleanupPath: unmanagedDirectory}})
	if _, err := os.Stat(unmanagedDirectory); err != nil {
		t.Errorf("unmanaged directory was removed or stat failed: %v", err)
	}
}

func TestCleanupDownloadedFilesRemovesBiginAttachmentTemporaryDirectory(t *testing.T) {
	workspaceDirectory := filepath.Join(t.TempDir(), "tmp")
	if err := os.MkdirAll(workspaceDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	temporaryDirectory, err := os.MkdirTemp(workspaceDirectory, "tourplannerbot-bigin-attachments-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	cleanupDownloadedFiles([]applicationTools.DownloadedFile{{CleanupPath: temporaryDirectory}})
	if _, err := os.Stat(temporaryDirectory); !os.IsNotExist(err) {
		t.Errorf("temporary directory still exists or stat failed: %v", err)
	}
}

func TestModelFileInputsSelectsSupportedBoundedFiles(t *testing.T) {
	fileInputs := modelFileInputs([]applicationTools.DownloadedFile{
		{Path: "/tmp/ticket.pdf", Filename: "ticket.pdf", MIMEType: "application/pdf", Size: 10},
		{Path: "/tmp/itinerary.docx", Filename: "itinerary.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Size: 10},
		{Path: "/tmp/notes.txt", Filename: "notes.txt", MIMEType: "text/plain", Size: 10},
		{Path: "/tmp/image.jpg", Filename: "image.jpg", MIMEType: "image/jpeg", Size: 10},
		{Path: "/tmp/large.pdf", Filename: "large.pdf", MIMEType: "application/pdf", Size: llm.MaximumInputFileBytes},
	})
	if len(fileInputs) != 3 || fileInputs[0].Path != "/tmp/ticket.pdf" || fileInputs[0].MIMEType != "application/pdf" || fileInputs[1].Path != "/tmp/itinerary.docx" || fileInputs[1].MIMEType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || fileInputs[2].Path != "/tmp/notes.txt" || fileInputs[2].MIMEType != "text/plain" {
		t.Errorf("model file inputs = %#v", fileInputs)
	}
}

func TestClearTransientFileInputsRemovesLocalFilePaths(t *testing.T) {
	clearedMessages := clearTransientFileInputs([]llm.Message{{
		Role: "user",
		FileInputs: []llm.FileInput{{
			Path:     "/tmp/original.pdf",
			Filename: "original.pdf",
			MIMEType: "application/pdf",
		}},
	}})
	if len(clearedMessages[0].FileInputs) != 0 {
		t.Errorf("file inputs = %#v, want none", clearedMessages[0].FileInputs)
	}
}
