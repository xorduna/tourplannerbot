// Package tools defines the provider-independent abstraction used by native and
// MCP-backed tools.
package tools

import (
	"context"
	"encoding/json"
)

// Definition describes a callable tool to an LLM.
type Definition struct {
	Name        string
	Description string
	Parameters  map[string]any
	Strict      bool
}

// Tool exposes a definition and executes one call using JSON arguments.
type Tool interface {
	Definition() Definition
	Execute(ctx context.Context, arguments json.RawMessage) (string, error)
}
