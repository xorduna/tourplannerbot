package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tourplannerbot/internal/tools"
)

func TestRenameFileRenamesQueuedDelivery(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "tmp")
	filesystemTool, err := New(Config{Root: workspaceRoot})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	temporaryDirectory, err := os.MkdirTemp(filesystemTool.Root(), "tourplannerbot-gmail-attachments-")
	if err != nil {
		t.Fatalf("MkdirTemp returned an error: %v", err)
	}
	sourcePath := filepath.Join(temporaryDirectory, "original.pdf")
	if err := os.WriteFile(sourcePath, []byte("PDF"), 0o600); err != nil {
		t.Fatalf("WriteFile returned an error: %v", err)
	}
	executionContext, err := tools.NewExecutionContext(-100123, 7, 99)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	executionContext.RecordDownloadedFile(tools.DownloadedFile{Path: sourcePath, Filename: "original.pdf", CleanupPath: temporaryDirectory})

	result, err := filesystemTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(`{"command":"rename_file","filename":"original.pdf","new_filename":"visita-sagrada-familia.pdf"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if result != `{"command":"rename_file","previous_filename":"original.pdf","filename":"visita-sagrada-familia.pdf","status":"renamed"}` {
		t.Errorf("result = %s", result)
	}
	queuedFiles := executionContext.DownloadedFiles()
	newPath := filepath.Join(temporaryDirectory, "visita-sagrada-familia.pdf")
	if len(queuedFiles) != 1 || queuedFiles[0].Filename != "visita-sagrada-familia.pdf" || queuedFiles[0].Path != newPath {
		t.Errorf("queued files = %#v", queuedFiles)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Errorf("source file still exists, stat error = %v", err)
	}
	if content, err := os.ReadFile(newPath); err != nil || string(content) != "PDF" {
		t.Errorf("renamed file = %q, error = %v", content, err)
	}
}

func TestRenameFileRejectsUnsafeOrUnavailableFiles(t *testing.T) {
	filesystemTool, err := New(Config{Root: filepath.Join(t.TempDir(), "tmp")})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	executionContext, _ := tools.NewExecutionContext(-100123, 7, 99)
	executionContext.RecordDownloadedFile(tools.DownloadedFile{Filename: "original.pdf"})
	for _, rawArguments := range []string{
		`{"command":"rename_file","filename":"original.pdf","new_filename":"../other.pdf"}`,
		`{"command":"rename_file","filename":"missing.pdf","new_filename":"other.pdf"}`,
		`{"command":"rename_file","filename":"original.pdf","new_filename":"original.pdf"}`,
		`{"command":"delete_file","filename":"original.pdf","new_filename":"other.pdf"}`,
	} {
		if _, err := filesystemTool.Execute(tools.WithExecutionContext(context.Background(), executionContext), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if _, err := filesystemTool.Execute(context.Background(), json.RawMessage(`{"command":"rename_file","filename":"original.pdf","new_filename":"other.pdf"}`)); err == nil || !strings.Contains(err.Error(), "trusted tool execution context") {
		t.Errorf("Execute without context error = %v", err)
	}
}

func TestDefinitionIsStrict(t *testing.T) {
	filesystemTool, err := New(Config{Root: filepath.Join(t.TempDir(), "tmp")})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	definition := filesystemTool.Definition()
	if definition.Name != toolName || definition.Source != "filesystem" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
}
