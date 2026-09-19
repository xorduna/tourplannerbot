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
	return registry.RegisterAll([]Tool{tool})
}

// RegisterAll atomically registers a set of tools. If any name is invalid or
// conflicts with the registry or another tool in the set, none are added.
func (registry *Registry) RegisterAll(newTools []Tool) error {
	newToolsByName := make(map[string]Tool, len(newTools))
	for _, newTool := range newTools {
		if newTool == nil {
			return fmt.Errorf("register tools: tool is nil")
		}
		definition := newTool.Definition()
		toolName := strings.TrimSpace(definition.Name)
		if toolName == "" {
			return fmt.Errorf("register tools: name is required")
		}
		if _, alreadyRegistered := registry.toolsByName[toolName]; alreadyRegistered {
			return fmt.Errorf("register tool %q: name is already registered", toolName)
		}
		if _, duplicatedInBatch := newToolsByName[toolName]; duplicatedInBatch {
			return fmt.Errorf("register tool %q: name is duplicated in registration batch", toolName)
		}
		newToolsByName[toolName] = newTool
	}
	for toolName, newTool := range newToolsByName {
		registry.toolsByName[toolName] = newTool
	}
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

// Source returns the internal source label for a registered tool. The label is
// application metadata and is not exposed in the LLM function definition.
func (registry *Registry) Source(name string) string {
	registeredTool, found := registry.toolsByName[name]
	if !found {
		return ""
	}
	return registeredTool.Definition().Source
}

// Execute invokes a registered tool by name.
func (registry *Registry) Execute(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	registeredTool, found := registry.toolsByName[name]
	if !found {
		return "", fmt.Errorf("tool %q is not registered", name)
	}
	return registeredTool.Execute(ctx, arguments)
}
