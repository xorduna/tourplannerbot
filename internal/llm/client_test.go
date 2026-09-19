package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	applicationTools "tourplannerbot/internal/tools"
)

type responseRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions"`
	Input           []inputMessage `json:"input"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	Store           bool           `json:"store"`
}

// TestClientGenerateReturnsToolCalls verifies function definitions are sent and
// function calls are returned without requiring text output.
func TestClientGenerateReturnsToolCalls(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var requestPayload struct {
			Tools []struct {
				Type        string         `json:"type"`
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
				Strict      bool           `json:"strict"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&requestPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(requestPayload.Tools) != 1 {
			t.Fatalf("tools length = %d, want 1", len(requestPayload.Tools))
		}
		requestTool := requestPayload.Tools[0]
		if requestTool.Type != "function" || requestTool.Name != "current_time" || requestTool.Description != "Get the current time." {
			t.Errorf("tool = %#v", requestTool)
		}
		if _, hasTopLevelOneOf := requestTool.Parameters["oneOf"]; hasTopLevelOneOf {
			t.Errorf("tool parameters still contain top-level oneOf: %#v", requestTool.Parameters)
		}
		if properties, propertiesAreObject := requestTool.Parameters["properties"].(map[string]any); !propertiesAreObject || len(properties) != 2 {
			t.Errorf("tool properties were not preserved: %#v", requestTool.Parameters["properties"])
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"id":"resp_tool","output":[{"type":"reasoning","id":"rs_123","summary":[],"encrypted_content":"encrypted-state","status":"completed"},{"type":"function_call","id":"fc_123","call_id":"call_123","name":"current_time","arguments":"{\"timezone\":\"Europe/Madrid\"}"}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}`))
	}))
	defer testServer.Close()

	client := NewClient("test-api-key", testServer.URL+"/v1", "openai", "gpt-5.5", 128, nil)
	generation, err := client.Generate(context.Background(), "Use tools.", []Message{{Role: "user", Content: "What time is it?"}}, []applicationTools.Definition{{
		Name:        "current_time",
		Description: "Get the current time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone":   map[string]any{"type": "string"},
				"utc_offset": map[string]any{"type": "string"},
			},
			"oneOf": []any{
				map[string]any{"required": []string{"timezone"}},
				map[string]any{"required": []string{"utc_offset"}},
			},
		},
	}})
	if err != nil {
		t.Fatalf("Generate returned an error: %v", err)
	}
	if generation.Text != "" {
		t.Errorf("Text = %q, want empty", generation.Text)
	}
	if len(generation.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(generation.ToolCalls))
	}
	if generation.ToolCalls[0].ID != "call_123" || generation.ToolCalls[0].Name != "current_time" || generation.ToolCalls[0].Arguments != `{"timezone":"Europe/Madrid"}` {
		t.Errorf("ToolCalls[0] = %#v", generation.ToolCalls[0])
	}
	if len(generation.ContinuationMessages) != 2 || generation.ContinuationMessages[0].Role != "reasoning" || generation.ContinuationMessages[1].ToolCallID != "call_123" {
		t.Errorf("ContinuationMessages = %#v", generation.ContinuationMessages)
	}
	var reasoningContinuation map[string]any
	if err := json.Unmarshal([]byte(generation.ContinuationMessages[0].Content), &reasoningContinuation); err != nil {
		t.Fatalf("decode reasoning continuation: %v", err)
	}
	if reasoningContinuation["id"] != "rs_123" || reasoningContinuation["encrypted_content"] != "encrypted-state" {
		t.Errorf("reasoning continuation = %#v", reasoningContinuation)
	}
}

// TestClientGenerateSerializesPersistedToolExchange verifies stored calls and
// results are reconstructed as Responses API input items.
func TestClientGenerateSerializesPersistedToolExchange(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var requestPayload struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&requestPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(requestPayload.Input) != 4 {
			t.Fatalf("input length = %d, want 4", len(requestPayload.Input))
		}
		if requestPayload.Input[1]["type"] != "reasoning" || requestPayload.Input[1]["id"] != "rs_123" || requestPayload.Input[1]["encrypted_content"] != "encrypted-state" {
			t.Errorf("reasoning input = %#v", requestPayload.Input[1])
		}
		if requestPayload.Input[2]["type"] != "function_call" || requestPayload.Input[2]["call_id"] != "call_123" || requestPayload.Input[2]["name"] != "current_time" {
			t.Errorf("function call input = %#v", requestPayload.Input[2])
		}
		if requestPayload.Input[3]["type"] != "function_call_output" || requestPayload.Input[3]["call_id"] != "call_123" || requestPayload.Input[3]["output"] != `{"local_time":"2026-09-19T12:00:00+02:00"}` {
			t.Errorf("function output input = %#v", requestPayload.Input[3])
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"id":"resp_final","output":[{"type":"message","content":[{"type":"output_text","text":"Són les dotze."}]}],"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":25}}`))
	}))
	defer testServer.Close()

	client := NewClient("test-api-key", testServer.URL+"/v1", "openai", "gpt-5.5", 128, nil)
	generation, err := client.Generate(context.Background(), "Use tools.", []Message{
		{Role: "user", Content: "Quina hora és?"},
		{Role: "reasoning", Content: `{"id":"rs_123","encrypted_content":"encrypted-state","status":"completed","summary":[]}`},
		{Role: "assistant", ToolCallID: "call_123", ToolName: "current_time", ToolArguments: `{}`},
		{Role: "tool", ToolCallID: "call_123", ToolName: "current_time", Content: `{"local_time":"2026-09-19T12:00:00+02:00"}`},
	}, nil)
	if err != nil {
		t.Fatalf("Generate returned an error: %v", err)
	}
	if generation.Text != "Són les dotze." {
		t.Errorf("Text = %q", generation.Text)
	}
}

type inputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TestClientGenerate verifies the OpenAI SDK request and text extraction.
func TestClientGenerate(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("request path = %q, want /v1/responses", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}

		var requestPayload responseRequest
		if err := json.NewDecoder(request.Body).Decode(&requestPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if requestPayload.Model != "gpt-5.5" {
			t.Errorf("model = %q, want gpt-5.5", requestPayload.Model)
		}
		if requestPayload.Instructions != "Tell a joke." {
			t.Errorf("instructions = %q", requestPayload.Instructions)
		}
		wantInput := []inputMessage{
			{Role: "user", Content: "Tell me something about cats."},
			{Role: "assistant", Content: "Cats are curious."},
			{Role: "user", Content: "Tell me a joke."},
		}
		if len(requestPayload.Input) != len(wantInput) {
			t.Fatalf("input length = %d, want %d", len(requestPayload.Input), len(wantInput))
		}
		for inputIndex, wantMessage := range wantInput {
			if requestPayload.Input[inputIndex] != wantMessage {
				t.Errorf("input[%d] = %#v, want %#v", inputIndex, requestPayload.Input[inputIndex], wantMessage)
			}
		}
		if requestPayload.MaxOutputTokens != 128 {
			t.Errorf("max_output_tokens = %d, want 128", requestPayload.MaxOutputTokens)
		}
		if requestPayload.Store {
			t.Error("store = true, want false")
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"id":"resp_123","output":[{"type":"message","content":[{"type":"output_text","text":"Per què el gat porta ordinador? Per caçar ratolins."}]}],"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":20},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10},"total_tokens":130}}`))
	}))
	defer testServer.Close()

	client := NewClient("test-api-key", testServer.URL+"/v1", "openai", "gpt-5.5", 128, &Pricing{
		InputMicroUSDPerMillion:       2_000_000,
		CachedInputMicroUSDPerMillion: 1_000_000,
		OutputMicroUSDPerMillion:      4_000_000,
		Version:                       "test-price-v1",
	})
	generation, err := client.Generate(context.Background(), "Tell a joke.", []Message{
		{Role: "user", Content: "Tell me something about cats."},
		{Role: "assistant", Content: "Cats are curious."},
		{Role: "user", Content: "Tell me a joke."},
	}, nil)
	if err != nil {
		t.Fatalf("Generate returned an error: %v", err)
	}
	if generation.Text != "Per què el gat porta ordinador? Per caçar ratolins." {
		t.Errorf("response text = %q", generation.Text)
	}
	if generation.ProviderResponseID != "resp_123" {
		t.Errorf("provider response ID = %q", generation.ProviderResponseID)
	}
	if generation.InputTokens != 100 || generation.CachedInputTokens != 20 || generation.OutputTokens != 30 || generation.ReasoningTokens != 10 || generation.TotalTokens != 130 {
		t.Errorf("usage = %#v", generation)
	}
	if generation.EstimatedCostMicroUSD == nil || *generation.EstimatedCostMicroUSD != 300 {
		t.Errorf("estimated cost = %v, want 300 micro-USD", generation.EstimatedCostMicroUSD)
	}
	if generation.PricingVersion != "test-price-v1" {
		t.Errorf("pricing version = %q", generation.PricingVersion)
	}
}
