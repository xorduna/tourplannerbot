package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	applicationTools "tourplannerbot/internal/tools"
)

type initializationTestTool struct{}

func (initializationTestTool) Definition() applicationTools.Definition {
	return applicationTools.Definition{
		Name:       "test_tool",
		Parameters: map[string]any{"type": "object"},
		Source:     "test-source",
	}
}

func (initializationTestTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return `{}`, nil
}

// TestInitializeAndRegisterToolLogsLifecycle verifies startup logs expose the
// initialization, registration, and ready phases for each available tool.
func TestInitializeAndRegisterToolLogsLifecycle(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelInfo}))
	toolRegistry := applicationTools.NewRegistry()

	err := initializeAndRegisterTool(logger, toolRegistry, "test_tool", "test-source", func() (applicationTools.Tool, error) {
		return initializationTestTool{}, nil
	})
	if err != nil {
		t.Fatalf("initializeAndRegisterTool returned an error: %v", err)
	}
	if toolRegistry.Source("test_tool") != "test-source" {
		t.Fatal("test tool was not registered")
	}

	encodedLogs := logOutput.String()
	for _, expectedFragment := range []string{
		`"msg":"initializing tool test_tool"`,
		`"phase":"initialize"`,
		`"msg":"registering tool test_tool"`,
		`"phase":"register"`,
		`"msg":"tool test_tool initialized and registered"`,
		`"status":"ready"`,
		`"tool_source":"test-source"`,
	} {
		if !strings.Contains(encodedLogs, expectedFragment) {
			t.Errorf("startup logs do not contain %s; logs: %s", expectedFragment, encodedLogs)
		}
	}
}
