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
	provider        string
	model           string
	maxOutputTokens int
	pricing         *Pricing
}

// Message is one text turn supplied as context to the Responses API.
type Message struct {
	Role    string
	Content string
}

// Pricing contains the immutable price snapshot used to estimate a request's
// cost. Rates are micro-USD per one million tokens.
type Pricing struct {
	InputMicroUSDPerMillion       int64
	CachedInputMicroUSDPerMillion int64
	OutputMicroUSDPerMillion      int64
	Version                       string
}

// Generation is the non-sensitive result metadata from one LLM request.
type Generation struct {
	Text                  string
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
// returns its text reply and metadata. Message roles must be user or assistant.
func (client *Client) Generate(ctx context.Context, instructions string, conversationMessages []Message) (generation Generation, err error) {
	startedAt := time.Now()
	generation.Provider = client.provider
	generation.Model = client.model
	defer func() { generation.Duration = time.Since(startedAt) }()

	inputItems := make(responses.ResponseInputParam, 0, len(conversationMessages))
	for _, conversationMessage := range conversationMessages {
		if conversationMessage.Role != "user" && conversationMessage.Role != "assistant" {
			return generation, fmt.Errorf("unsupported conversation message role: %s", conversationMessage.Role)
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

	responseText := strings.TrimSpace(response.OutputText())
	if responseText == "" {
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
