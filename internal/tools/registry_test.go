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
	return Definition{Name: tool.name, Parameters: map[string]any{"type": "object"}, Source: "test-source"}
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

// TestRegistrySourceReturnsInternalMetadata verifies that callers can identify
// a tool provider without exposing that metadata to the LLM tool contract.
func TestRegistrySourceReturnsInternalMetadata(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testTool{name: "search_places"}); err != nil {
		t.Fatalf("Register returned an error: %v", err)
	}
	if actualSource := registry.Source("search_places"); actualSource != "test-source" {
		t.Fatalf("Source returned %q, want %q", actualSource, "test-source")
	}
	if actualSource := registry.Source("missing"); actualSource != "" {
		t.Fatalf("Source for missing tool returned %q, want empty", actualSource)
	}
}

// TestRegistryRegisterAllIsAtomic verifies a batch conflict does not leave
// earlier tools from that same batch registered.
func TestRegistryRegisterAllIsAtomic(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testTool{name: "current_time"}); err != nil {
		t.Fatalf("Register returned an error: %v", err)
	}
	registrationError := registry.RegisterAll([]Tool{
		testTool{name: "wikipedia_search"},
		testTool{name: "current_time"},
	})
	if registrationError == nil {
		t.Fatal("RegisterAll returned nil error for a conflicting batch")
	}
	if len(registry.Definitions()) != 1 {
		t.Errorf("len(Definitions) = %d, want 1 after rejected batch", len(registry.Definitions()))
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
