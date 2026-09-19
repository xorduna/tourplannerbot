package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Value string `json:"value" jsonschema:"the value to echo"`
}

type echoOutput struct {
	Echo string `json:"echo"`
}

// TestConnectDiscoversAndExecutesTools verifies the adapter against a real
// Streamable HTTP MCP server and checks bearer authentication on every request.
func TestConnectDiscoversAndExecutesTools(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "echo", Description: "Echo a value."}, func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
		return nil, echoOutput{Echo: input.Value}, nil
	})
	streamableHandler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	authenticatedHandler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(responseWriter, "unauthorized", http.StatusUnauthorized)
			return
		}
		streamableHandler.ServeHTTP(responseWriter, request)
	})
	httpServer := httptest.NewServer(authenticatedHandler)
	defer httpServer.Close()

	connectionContext, cancelConnectionContext := context.WithCancel(context.Background())
	connection, err := Connect(connectionContext, Config{
		Name:           "test",
		URL:            httpServer.URL,
		AuthType:       "bearer",
		Token:          "test-token",
		RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Connect returned an error: %v", err)
	}
	cancelConnectionContext()
	defer connection.Close()

	discoveredTools := connection.Tools()
	if len(discoveredTools) != 1 || discoveredTools[0].Definition().Name != "echo" {
		t.Fatalf("Tools = %#v, want one echo tool", discoveredTools)
	}
	if discoveredTools[0].Definition().Source != "test" {
		t.Fatalf("tool source = %q, want %q", discoveredTools[0].Definition().Source, "test")
	}
	toolResult, err := discoveredTools[0].Execute(context.Background(), []byte(`{"value":"hello"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	if toolResult != `{"echo":"hello"}` {
		t.Errorf("Execute result = %s, want structured echo JSON", toolResult)
	}
}

// TestNormalizeInputSchemaRejectsNonObjectSchema verifies MCP tools only expose
// object-shaped argument schemas through the common registry.
func TestNormalizeInputSchemaRejectsNonObjectSchema(t *testing.T) {
	if _, err := normalizeInputSchema([]string{"not", "an", "object"}); err == nil {
		t.Fatal("normalizeInputSchema returned nil error for an array schema")
	}
	if _, err := normalizeInputSchema(map[string]any{"type": "string"}); err == nil {
		t.Fatal("normalizeInputSchema returned nil error for a string schema")
	}
}
