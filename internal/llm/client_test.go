package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type responseRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions"`
	Input           []inputMessage `json:"input"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	Store           bool           `json:"store"`
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
		_, _ = responseWriter.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"Per què el gat porta ordinador? Per caçar ratolins."}]}]}`))
	}))
	defer testServer.Close()

	client := NewClient("test-api-key", testServer.URL+"/v1", "gpt-5.5", 128)
	responseText, err := client.Generate(context.Background(), "Tell a joke.", []Message{
		{Role: "user", Content: "Tell me something about cats."},
		{Role: "assistant", Content: "Cats are curious."},
		{Role: "user", Content: "Tell me a joke."},
	})
	if err != nil {
		t.Fatalf("Generate returned an error: %v", err)
	}
	if responseText != "Per què el gat porta ordinador? Per caçar ratolins." {
		t.Errorf("response text = %q", responseText)
	}
}
