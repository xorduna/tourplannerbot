package brave

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"

	"tourplannerbot/internal/tools"
)

const (
	webSearchToolName        = "web_search"
	maximumQueryLength       = 400
	maximumQueryWordCount    = 50
	maximumResultCount       = 20
	maximumDescriptionLength = 600
)

var (
	highlightMarkupPattern = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	collapsibleWhitespace  = regexp.MustCompile(`\s+`)
	supportedFreshnessKeys = map[string]bool{
		"pd": true,
		"pw": true,
		"pm": true,
		"py": true,
	}
)

// WebSearchTool searches the public web through the Brave Search API and
// returns a compact result list for the model.
type WebSearchTool struct {
	client             *Client
	defaultResultCount int
}

// braveWebSearchEnvelope contains only the Brave response fields the tool
// forwards to the model. The remaining envelope is deliberately discarded so a
// single search does not consume an unreasonable part of the context window.
type braveWebSearchEnvelope struct {
	Query struct {
		Original string `json:"original"`
	} `json:"query"`
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Age         string `json:"age"`
			PageAge     string `json:"page_age"`
		} `json:"results"`
	} `json:"web"`
}

// webSearchResult is one compacted search hit returned to the model.
type webSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Age         string `json:"age,omitempty"`
}

// NewWebSearch creates the Brave web search tool and validates its default
// result count.
func NewWebSearch(client *Client, defaultResultCount int) (*WebSearchTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Brave client is required")
	}
	if defaultResultCount <= 0 || defaultResultCount > maximumResultCount {
		return nil, fmt.Errorf("Brave default result count must be between 1 and %d", maximumResultCount)
	}
	return &WebSearchTool{client: client, defaultResultCount: defaultResultCount}, nil
}

// Definition describes the web_search function to the LLM.
func (webSearchTool *WebSearchTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        webSearchToolName,
		Description: "Search the public web with Brave Search and return the most relevant page titles, URLs, and snippets. Use it for current or practical information such as opening hours, prices, ticket availability, events, transport, restaurants, or news, and always prefer it over answering from memory when the answer can change over time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": fmt.Sprintf("The search query in the language best suited to the expected sources. At most %d characters and %d words.", maximumQueryLength, maximumQueryWordCount),
				},
				"count": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     maximumResultCount,
					"description": fmt.Sprintf("Optional number of results to return. Defaults to %d.", webSearchTool.defaultResultCount),
				},
				"freshness": map[string]any{
					"type":        "string",
					"enum":        []string{"pd", "pw", "pm", "py"},
					"description": "Optional recency filter: pd for the last day, pw for the last week, pm for the last month, or py for the last year. Omit it for general questions.",
				},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
		Strict: false,
		Source: "brave",
	}
}

// Execute validates the model arguments, performs one Brave web search, and
// returns a compact JSON object containing the effective query and results.
func (webSearchTool *WebSearchTool) Execute(applicationContext context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		Query     string `json:"query"`
		Count     *int   `json:"count"`
		Freshness string `json:"freshness"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", webSearchToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", webSearchToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", webSearchToolName, err)
	}

	searchQuery := strings.TrimSpace(arguments.Query)
	if searchQuery == "" {
		return "", fmt.Errorf("query is required")
	}
	if len(searchQuery) > maximumQueryLength {
		return "", fmt.Errorf("query must be at most %d characters", maximumQueryLength)
	}
	if len(strings.Fields(searchQuery)) > maximumQueryWordCount {
		return "", fmt.Errorf("query must be at most %d words", maximumQueryWordCount)
	}

	requestedResultCount := webSearchTool.defaultResultCount
	if arguments.Count != nil {
		requestedResultCount = *arguments.Count
		if requestedResultCount < 1 || requestedResultCount > maximumResultCount {
			return "", fmt.Errorf("count must be between 1 and %d", maximumResultCount)
		}
	}

	queryValues := url.Values{
		"q":     {searchQuery},
		"count": {fmt.Sprintf("%d", requestedResultCount)},
	}
	requestedFreshness := strings.ToLower(strings.TrimSpace(arguments.Freshness))
	if requestedFreshness != "" {
		if !supportedFreshnessKeys[requestedFreshness] {
			return "", fmt.Errorf("freshness must be one of pd, pw, pm, or py")
		}
		queryValues.Set("freshness", requestedFreshness)
	}

	responseBody, err := webSearchTool.client.get(applicationContext, "/res/v1/web/search", queryValues)
	if err != nil {
		return "", err
	}

	var responseEnvelope braveWebSearchEnvelope
	if err := json.Unmarshal(responseBody, &responseEnvelope); err != nil {
		return "", fmt.Errorf("decode Brave Search response: %w", err)
	}

	compactedResults := make([]webSearchResult, 0, len(responseEnvelope.Web.Results))
	for _, braveResult := range responseEnvelope.Web.Results {
		resultURL := strings.TrimSpace(braveResult.URL)
		if resultURL == "" {
			continue
		}
		resultAge := strings.TrimSpace(braveResult.Age)
		if resultAge == "" {
			resultAge = strings.TrimSpace(braveResult.PageAge)
		}
		compactedResults = append(compactedResults, webSearchResult{
			Title:       plainText(braveResult.Title, 0),
			URL:         resultURL,
			Description: plainText(braveResult.Description, maximumDescriptionLength),
			Age:         resultAge,
		})
		if len(compactedResults) == requestedResultCount {
			break
		}
	}

	effectiveQuery := strings.TrimSpace(responseEnvelope.Query.Original)
	if effectiveQuery == "" {
		effectiveQuery = searchQuery
	}
	result := struct {
		Query   string            `json:"query"`
		Count   int               `json:"count"`
		Results []webSearchResult `json:"results"`
	}{
		Query:   effectiveQuery,
		Count:   len(compactedResults),
		Results: compactedResults,
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode %s result: %w", webSearchToolName, err)
	}
	return string(encodedResult), nil
}

// plainText removes the highlight markup and HTML entities Brave includes in
// titles and snippets, collapses whitespace, and optionally truncates the
// result. A maximumLength of zero keeps the complete text.
func plainText(rawText string, maximumLength int) string {
	unmarkedText := highlightMarkupPattern.ReplaceAllString(rawText, "")
	decodedText := html.UnescapeString(unmarkedText)
	normalizedText := strings.TrimSpace(collapsibleWhitespace.ReplaceAllString(decodedText, " "))
	if maximumLength > 0 && len(normalizedText) > maximumLength {
		truncatedText := normalizedText[:maximumLength]
		for len(truncatedText) > 0 && !isUTF8Boundary(normalizedText, len(truncatedText)) {
			truncatedText = truncatedText[:len(truncatedText)-1]
		}
		return strings.TrimSpace(truncatedText) + "…"
	}
	return normalizedText
}

// isUTF8Boundary reports whether an index falls between two complete UTF-8
// runes so truncation never splits a multi-byte character.
func isUTF8Boundary(text string, index int) bool {
	if index <= 0 || index >= len(text) {
		return true
	}
	return text[index]&0xC0 != 0x80
}
