package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type testTool struct {
	name string
}

func (tool testTool) Definition() Definition {
	return Definition{Name: tool.name, Parameters: map[string]any{"type": "object"}}
}

func (tool testTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return tool.name, nil
}

// TestRegistryRejectsDuplicateNames verifies that one tool cannot shadow another.
func TestRegistryRejectsDuplicateNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testTool{name: "current_time"}); err != nil {
		t.Fatalf("first Register returned an error: %v", err)
	}
	if err := registry.Register(testTool{name: "current_time"}); err == nil {
		t.Fatal("duplicate Register returned nil error")
	}
}

// TestRegistryReturnsSortedDefinitions verifies deterministic tool ordering.
func TestRegistryReturnsSortedDefinitions(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(testTool{name: "wikipedia"})
	_ = registry.Register(testTool{name: "current_time"})
	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "current_time" || definitions[1].Name != "wikipedia" {
		t.Errorf("Definitions = %#v", definitions)
	}
}
