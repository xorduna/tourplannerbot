// Package mcpclient adapts tools discovered from Streamable HTTP MCP servers
// to the application's provider-independent tool interface.
package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	applicationTools "tourplannerbot/internal/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config contains the settings needed to connect to one MCP server.
type Config struct {
	Name           string
	URL            string
	AuthType       string
	Token          string
	RequestTimeout time.Duration
}

// Connection owns one MCP client session and its discovered tool adapters.
type Connection struct {
	serverName      string
	clientSession   *mcp.ClientSession
	discoveredTools []applicationTools.Tool
}

// Connect initializes a Streamable HTTP MCP session, discovers every page of
// tools, and returns adapters ready for registration. The caller must close a
// successful connection.
func Connect(ctx context.Context, configuration Config) (*Connection, error) {
	httpClient := &http.Client{}
	if configuration.AuthType == "bearer" {
		httpClient.Transport = &bearerTokenRoundTripper{
			baseRoundTripper: http.DefaultTransport,
			token:            configuration.Token,
		}
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "tourplannerbot",
		Version: "0.1.0",
	}, nil)
	clientSession, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             configuration.URL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server %q: %w", configuration.Name, err)
	}

	connection := &Connection{
		serverName:    configuration.Name,
		clientSession: clientSession,
	}
	discoveredTools, err := connection.discoverTools(ctx, configuration.RequestTimeout)
	if err != nil {
		_ = clientSession.Close()
		return nil, err
	}
	connection.discoveredTools = discoveredTools
	return connection, nil
}

// Tools returns the immutable set of tool adapters discovered during Connect.
func (connection *Connection) Tools() []applicationTools.Tool {
	toolsCopy := make([]applicationTools.Tool, len(connection.discoveredTools))
	copy(toolsCopy, connection.discoveredTools)
	return toolsCopy
}

// Close closes the underlying MCP client session.
func (connection *Connection) Close() error {
	if connection == nil || connection.clientSession == nil {
		return nil
	}
	return connection.clientSession.Close()
}

// discoverTools follows MCP pagination and converts each advertised tool to an
// application tool while preserving its original, unprefixed name.
func (connection *Connection) discoverTools(ctx context.Context, requestTimeout time.Duration) ([]applicationTools.Tool, error) {
	discoveredTools := make([]applicationTools.Tool, 0)
	nextCursor := ""
	for {
		requestContext, cancelRequest := context.WithTimeout(ctx, requestTimeout)
		listResult, err := connection.clientSession.ListTools(requestContext, &mcp.ListToolsParams{Cursor: nextCursor})
		cancelRequest()
		if err != nil {
			return nil, fmt.Errorf("list tools from MCP server %q: %w", connection.serverName, err)
		}
		for _, discoveredTool := range listResult.Tools {
			adaptedTool, err := newRemoteTool(connection.serverName, connection.clientSession, discoveredTool, requestTimeout)
			if err != nil {
				return nil, err
			}
			discoveredTools = append(discoveredTools, adaptedTool)
		}
		if listResult.NextCursor == "" {
			break
		}
		nextCursor = listResult.NextCursor
	}
	return discoveredTools, nil
}

// remoteTool forwards one provider-independent tool call to an MCP session.
type remoteTool struct {
	serverName     string
	clientSession  *mcp.ClientSession
	definition     applicationTools.Definition
	requestTimeout time.Duration
}

// newRemoteTool validates an advertised MCP tool and creates its adapter.
func newRemoteTool(serverName string, clientSession *mcp.ClientSession, discoveredTool *mcp.Tool, requestTimeout time.Duration) (*remoteTool, error) {
	if discoveredTool == nil {
		return nil, fmt.Errorf("MCP server %q advertised a nil tool", serverName)
	}
	toolName := strings.TrimSpace(discoveredTool.Name)
	if toolName == "" {
		return nil, fmt.Errorf("MCP server %q advertised a tool without a name", serverName)
	}
	parameters, err := normalizeInputSchema(discoveredTool.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("MCP server %q tool %q has an invalid input schema: %w", serverName, toolName, err)
	}
	return &remoteTool{
		serverName:    serverName,
		clientSession: clientSession,
		definition: applicationTools.Definition{
			Name:        toolName,
			Description: discoveredTool.Description,
			Parameters:  parameters,
			Strict:      false,
			Source:      serverName,
		},
		requestTimeout: requestTimeout,
	}, nil
}

// Definition returns the MCP tool definition in the application's common
// representation.
func (tool *remoteTool) Definition() applicationTools.Definition {
	return tool.definition
}

// Execute decodes JSON object arguments, invokes the remote MCP tool, and
// serializes its structured or unstructured result for the LLM.
func (tool *remoteTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := make(map[string]any)
	if len(rawArguments) > 0 {
		if err := json.Unmarshal(rawArguments, &arguments); err != nil {
			return "", fmt.Errorf("decode arguments for MCP tool %q: %w", tool.definition.Name, err)
		}
	}

	requestContext, cancelRequest := context.WithTimeout(ctx, tool.requestTimeout)
	defer cancelRequest()
	callResult, err := tool.clientSession.CallTool(requestContext, &mcp.CallToolParams{
		Name:      tool.definition.Name,
		Arguments: arguments,
	})
	if err != nil {
		return "", fmt.Errorf("call MCP server %q tool %q: %w", tool.serverName, tool.definition.Name, err)
	}
	serializedResult, err := serializeCallResult(callResult)
	if err != nil {
		return "", fmt.Errorf("serialize MCP server %q tool %q result: %w", tool.serverName, tool.definition.Name, err)
	}
	if callResult.IsError {
		return "", fmt.Errorf("MCP server %q tool %q reported an error: %s", tool.serverName, tool.definition.Name, serializedResult)
	}
	return serializedResult, nil
}

// normalizeInputSchema converts any JSON-marshallable schema advertised by an
// MCP server into the map required by the LLM abstraction.
func normalizeInputSchema(inputSchema any) (map[string]any, error) {
	if inputSchema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	serializedSchema, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, err
	}
	var normalizedSchema map[string]any
	if err := json.Unmarshal(serializedSchema, &normalizedSchema); err != nil {
		return nil, err
	}
	if normalizedSchema == nil {
		return nil, fmt.Errorf("schema must be a JSON object")
	}
	if schemaType, hasSchemaType := normalizedSchema["type"]; hasSchemaType && schemaType != "object" {
		return nil, fmt.Errorf("schema type must be object, got %v", schemaType)
	}
	normalizedSchema["type"] = "object"
	return normalizedSchema, nil
}

// serializeCallResult prefers MCP structured content, preserves a sole text
// response as plain text, and JSON-encodes mixed or non-text content.
func serializeCallResult(callResult *mcp.CallToolResult) (string, error) {
	if callResult == nil {
		return "", fmt.Errorf("MCP server returned an empty result")
	}
	if callResult.StructuredContent != nil {
		serializedStructuredContent, err := json.Marshal(callResult.StructuredContent)
		if err != nil {
			return "", err
		}
		return string(serializedStructuredContent), nil
	}
	if len(callResult.Content) == 1 {
		if textContent, isTextContent := callResult.Content[0].(*mcp.TextContent); isTextContent {
			return textContent.Text, nil
		}
	}
	serializedContent, err := json.Marshal(callResult.Content)
	if err != nil {
		return "", err
	}
	return string(serializedContent), nil
}

// bearerTokenRoundTripper adds bearer authentication without mutating the
// request shared with callers or middleware.
type bearerTokenRoundTripper struct {
	baseRoundTripper http.RoundTripper
	token            string
}

// RoundTrip clones the outgoing request and attaches its Authorization header.
func (roundTripper *bearerTokenRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clonedRequest := request.Clone(request.Context())
	clonedRequest.Header = request.Header.Clone()
	clonedRequest.Header.Set("Authorization", "Bearer "+roundTripper.token)
	return roundTripper.baseRoundTripper.RoundTrip(clonedRequest)
}
