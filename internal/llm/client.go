// Package llm contains the OpenAI client used to generate bot replies.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tourplannerbot/internal/tools"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

// Client generates text with the OpenAI Responses API.
type Client struct {
	openAIClient    openai.Client
	provider        string
	model           string
	maxOutputTokens int
	pricing         *Pricing
}

// Message is one persisted text, reasoning, function-call, or function-output
// item supplied as context to the Responses API.
type Message struct {
	Role          string
	Content       string
	ToolCallID    string
	ToolName      string
	ToolArguments string
}

// ToolCall is one function invocation requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Pricing contains the immutable price snapshot used to estimate a request's
// cost. Rates are micro-USD per one million tokens.
type Pricing struct {
	InputMicroUSDPerMillion       int64
	CachedInputMicroUSDPerMillion int64
	OutputMicroUSDPerMillion      int64
	Version                       string
}

// Generation contains the response content and audit metadata from one LLM request.
type Generation struct {
	Text                  string
	ToolCalls             []ToolCall
	ContinuationMessages  []Message
	Provider              string
	Model                 string
	ProviderResponseID    string
	InputTokens           int64
	CachedInputTokens     int64
	OutputTokens          int64
	ReasoningTokens       int64
	TotalTokens           int64
	UsageAvailable        bool
	EstimatedCostMicroUSD *int64
	PricingVersion        string
	Duration              time.Duration
}

// NewClient creates an OpenAI client using the supplied connection settings.
func NewClient(apiKey string, baseURL string, provider string, model string, maxOutputTokens int, pricing *Pricing) *Client {
	openAIClient := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(strings.TrimRight(baseURL, "/")),
		option.WithHTTPClient(&http.Client{Timeout: time.Minute}),
	)

	return &Client{
		openAIClient:    openAIClient,
		provider:        provider,
		model:           model,
		maxOutputTokens: maxOutputTokens,
		pricing:         pricing,
	}
}

// Generate sends instructions and conversationMessages to the Responses API and
// returns its text, tool calls, continuation items, and metadata.
func (client *Client) Generate(ctx context.Context, instructions string, conversationMessages []Message, toolDefinitions []tools.Definition) (generation Generation, err error) {
	startedAt := time.Now()
	generation.Provider = client.provider
	generation.Model = client.model
	defer func() { generation.Duration = time.Since(startedAt) }()

	inputItems := make(responses.ResponseInputParam, 0, len(conversationMessages))
	for _, conversationMessage := range conversationMessages {
		switch conversationMessage.Role {
		case "user", "assistant":
			if conversationMessage.ToolCallID != "" {
				if conversationMessage.Role != "assistant" || conversationMessage.ToolName == "" || conversationMessage.ToolArguments == "" {
					return generation, fmt.Errorf("invalid persisted tool call %q", conversationMessage.ToolCallID)
				}
				inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCall(
					conversationMessage.ToolArguments,
					conversationMessage.ToolCallID,
					conversationMessage.ToolName,
				))
				continue
			}
			inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
				conversationMessage.Content,
				responses.EasyInputMessageRole(conversationMessage.Role),
			))
		case "tool":
			if conversationMessage.ToolCallID == "" {
				return generation, fmt.Errorf("tool result is missing its call ID")
			}
			inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCallOutput(
				conversationMessage.ToolCallID,
				conversationMessage.Content,
			))
		case "reasoning":
			var reasoningItem struct {
				ID               string   `json:"id"`
				EncryptedContent string   `json:"encrypted_content"`
				Status           string   `json:"status"`
				Summary          []string `json:"summary"`
			}
			if err := json.Unmarshal([]byte(conversationMessage.Content), &reasoningItem); err != nil {
				return generation, fmt.Errorf("decode persisted reasoning item: %w", err)
			}
			if reasoningItem.ID == "" {
				return generation, fmt.Errorf("persisted reasoning item is missing its ID")
			}
			reasoningSummary := make([]responses.ResponseReasoningItemSummaryParam, 0, len(reasoningItem.Summary))
			for _, summaryText := range reasoningItem.Summary {
				reasoningSummary = append(reasoningSummary, responses.ResponseReasoningItemSummaryParam{Text: summaryText})
			}
			reasoningParameter := responses.ResponseReasoningItemParam{
				ID:      reasoningItem.ID,
				Summary: reasoningSummary,
				Status:  responses.ResponseReasoningItemStatus(reasoningItem.Status),
			}
			if reasoningItem.EncryptedContent != "" {
				reasoningParameter.EncryptedContent = openai.String(reasoningItem.EncryptedContent)
			}
			inputItems = append(inputItems, responses.ResponseInputItemUnionParam{OfReasoning: &reasoningParameter})
		default:
			return generation, fmt.Errorf("unsupported conversation message role: %s", conversationMessage.Role)
		}
	}

	responseTools := make([]responses.ToolUnionParam, 0, len(toolDefinitions))
	for _, toolDefinition := range toolDefinitions {
		normalizedParameters, normalizationError := normalizeFunctionParameters(toolDefinition.Parameters)
		if normalizationError != nil {
			return generation, fmt.Errorf("normalize parameters for tool %q: %w", toolDefinition.Name, normalizationError)
		}
		responseTool := responses.ToolParamOfFunction(toolDefinition.Name, normalizedParameters, toolDefinition.Strict)
		responseTool.OfFunction.Description = openai.String(toolDefinition.Description)
		responseTools = append(responseTools, responseTool)
	}

	response, err := client.openAIClient.Responses.New(ctx, responses.ResponseNewParams{
		Model:           client.model,
		Instructions:    openai.String(instructions),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
		MaxOutputTokens: openai.Int(int64(client.maxOutputTokens)),
		Store:           openai.Bool(false),
		Tools:           responseTools,
		Include:         []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
	})
	if err != nil {
		return generation, fmt.Errorf("generate OpenAI response: %w", err)
	}
	generation.ProviderResponseID = response.ID
	generation.InputTokens = response.Usage.InputTokens
	generation.CachedInputTokens = response.Usage.InputTokensDetails.CachedTokens
	generation.OutputTokens = response.Usage.OutputTokens
	generation.ReasoningTokens = response.Usage.OutputTokensDetails.ReasoningTokens
	generation.TotalTokens = response.Usage.TotalTokens
	generation.UsageAvailable = true
	if client.pricing != nil {
		cost := estimateCostMicroUSD(response.Usage, *client.pricing)
		generation.EstimatedCostMicroUSD = &cost
		generation.PricingVersion = client.pricing.Version
	}
	if response.Error.Message != "" {
		return generation, fmt.Errorf("OpenAI response failed: %s", response.Error.Message)
	}

	for _, outputItem := range response.Output {
		switch outputItem.Type {
		case "reasoning":
			reasoningItem := outputItem.AsReasoning()
			reasoningSummary := make([]string, 0, len(reasoningItem.Summary))
			for _, summaryPart := range reasoningItem.Summary {
				reasoningSummary = append(reasoningSummary, summaryPart.Text)
			}
			encodedReasoningItem, encodingError := json.Marshal(struct {
				ID               string   `json:"id"`
				EncryptedContent string   `json:"encrypted_content,omitempty"`
				Status           string   `json:"status,omitempty"`
				Summary          []string `json:"summary"`
			}{
				ID:               reasoningItem.ID,
				EncryptedContent: reasoningItem.EncryptedContent,
				Status:           string(reasoningItem.Status),
				Summary:          reasoningSummary,
			})
			if encodingError != nil {
				return generation, fmt.Errorf("encode reasoning continuation: %w", encodingError)
			}
			generation.ContinuationMessages = append(generation.ContinuationMessages, Message{
				Role:    "reasoning",
				Content: string(encodedReasoningItem),
			})
		case "function_call":
			functionCall := outputItem.AsFunctionCall()
			toolCall := ToolCall{
				ID:        functionCall.CallID,
				Name:      functionCall.Name,
				Arguments: functionCall.Arguments,
			}
			generation.ToolCalls = append(generation.ToolCalls, toolCall)
			generation.ContinuationMessages = append(generation.ContinuationMessages, Message{
				Role:          "assistant",
				ToolCallID:    toolCall.ID,
				ToolName:      toolCall.Name,
				ToolArguments: toolCall.Arguments,
			})
		}
	}

	responseText := strings.TrimSpace(response.OutputText())
	if responseText == "" && len(generation.ToolCalls) == 0 {
		return generation, fmt.Errorf("OpenAI response returned no text output")
	}

	generation.Text = responseText

	return generation, nil
}

func estimateCostMicroUSD(usage responses.ResponseUsage, pricing Pricing) int64 {
	const tokensPerMillion = int64(1_000_000)
	uncachedInputTokens := usage.InputTokens - usage.InputTokensDetails.CachedTokens
	return (uncachedInputTokens*pricing.InputMicroUSDPerMillion)/tokensPerMillion +
		(usage.InputTokensDetails.CachedTokens*pricing.CachedInputMicroUSDPerMillion)/tokensPerMillion +
		(usage.OutputTokens*pricing.OutputMicroUSDPerMillion)/tokensPerMillion
}
