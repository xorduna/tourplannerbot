package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Registry stores all tools available to the application.
type Registry struct {
	toolsByName map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{toolsByName: make(map[string]Tool)}
}

// Register adds one tool and rejects invalid or duplicate names.
func (registry *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("register tool: tool is nil")
	}
	definition := tool.Definition()
	if strings.TrimSpace(definition.Name) == "" {
		return fmt.Errorf("register tool: name is required")
	}
	if _, alreadyRegistered := registry.toolsByName[definition.Name]; alreadyRegistered {
		return fmt.Errorf("register tool %q: name is already registered", definition.Name)
	}
	registry.toolsByName[definition.Name] = tool
	return nil
}

// Definitions returns all registered tool definitions ordered by name.
func (registry *Registry) Definitions() []Definition {
	definitions := make([]Definition, 0, len(registry.toolsByName))
	for _, registeredTool := range registry.toolsByName {
		definitions = append(definitions, registeredTool.Definition())
	}
	sort.Slice(definitions, func(firstIndex int, secondIndex int) bool {
		return definitions[firstIndex].Name < definitions[secondIndex].Name
	})
	return definitions
}

// Execute invokes a registered tool by name.
func (registry *Registry) Execute(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	registeredTool, found := registry.toolsByName[name]
	if !found {
		return "", fmt.Errorf("tool %q is not registered", name)
	}
	return registeredTool.Execute(ctx, arguments)
}
