package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTrimsPrompt(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "system_query.md")
	if err := os.WriteFile(filename, []byte("\n Prompt text. \n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	loadedPrompt, err := Load(filename)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if loadedPrompt != "Prompt text." {
		t.Errorf("Load = %q, want Prompt text.", loadedPrompt)
	}
}

func TestLoadRejectsEmptyPrompt(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "system_query.md")
	if err := os.WriteFile(filename, []byte(" \n"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	if _, err := Load(filename); err == nil {
		t.Error("Load returned nil error for an empty prompt")
	}
}
