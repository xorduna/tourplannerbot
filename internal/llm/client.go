// Package llm contains the OpenAI client used to generate bot replies.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

// Client generates text with the OpenAI Responses API.
type Client struct {
	openAIClient    openai.Client
	model           string
	maxOutputTokens int
}

// Message is one text turn supplied as context to the Responses API.
type Message struct {
	Role    string
	Content string
}

// NewClient creates an OpenAI client using the supplied connection settings.
func NewClient(apiKey string, baseURL string, model string, maxOutputTokens int) *Client {
	openAIClient := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(strings.TrimRight(baseURL, "/")),
		option.WithHTTPClient(&http.Client{Timeout: time.Minute}),
	)

	return &Client{
		openAIClient:    openAIClient,
		model:           model,
		maxOutputTokens: maxOutputTokens,
	}
}

// Generate sends instructions and conversationMessages to the Responses API and
// returns its text reply. Message roles must be user or assistant.
func (client *Client) Generate(ctx context.Context, instructions string, conversationMessages []Message) (string, error) {
	inputItems := make(responses.ResponseInputParam, 0, len(conversationMessages))
	for _, conversationMessage := range conversationMessages {
		if conversationMessage.Role != "user" && conversationMessage.Role != "assistant" {
			return "", fmt.Errorf("unsupported conversation message role: %s", conversationMessage.Role)
		}
		inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
			conversationMessage.Content,
			responses.EasyInputMessageRole(conversationMessage.Role),
		))
	}

	response, err := client.openAIClient.Responses.New(ctx, responses.ResponseNewParams{
		Model:           client.model,
		Instructions:    openai.String(instructions),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
		MaxOutputTokens: openai.Int(int64(client.maxOutputTokens)),
		Store:           openai.Bool(false),
	})
	if err != nil {
		return "", fmt.Errorf("generate OpenAI response: %w", err)
	}
	if response.Error.Message != "" {
		return "", fmt.Errorf("OpenAI response failed: %s", response.Error.Message)
	}

	responseText := strings.TrimSpace(response.OutputText())
	if responseText == "" {
		return "", fmt.Errorf("OpenAI response returned no text output")
	}

	return responseText, nil
}
