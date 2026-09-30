package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	searchGmailMessagesToolName = "search_gmail_messages"
	defaultSearchResultCount    = 10
	maximumSearchResultCount    = 20
	maximumSearchTermLength     = 500
	maximumSearchPageTokenSize  = 1000
	maximumSearchSnippetLength  = 600
)

// SearchMessagesTool searches the configured mailbox and returns just enough
// metadata to identify which message should be read or downloaded next.
type SearchMessagesTool struct {
	client *Client
}

// NewSearchMessages creates the Gmail message-search tool.
func NewSearchMessages(client *Client) (*SearchMessagesTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Gmail client is required")
	}
	return &SearchMessagesTool{client: client}, nil
}

// Definition describes search_gmail_messages to the LLM. The four filters
// are intentionally separate so callers cannot inject arbitrary Gmail query
// operators or accidentally broaden a mailbox search.
func (searchMessagesTool *SearchMessagesTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        searchGmailMessagesToolName,
		Description: "Search messages in the configured Gmail mailbox by sender, recipient, subject, and/or text in the message. Use the returned message_id to identify a message for a later read or attachment-download action. This tool only searches and returns compact metadata; it never changes or sends email.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{
					"type":        "string",
					"description": "Optional sender email address, name, or text to match.",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "Optional recipient email address, name, or text to match.",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Optional subject text to match.",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Optional text or phrase to find in the message. Gmail performs this as its normal full-message text search.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     maximumSearchResultCount,
					"description": fmt.Sprintf("Optional number of messages to return. Defaults to %d.", defaultSearchResultCount),
				},
				"page_token": map[string]any{
					"type":        "string",
					"description": "Optional opaque next_page_token returned by an earlier search, to retrieve its next page with the same filters.",
				},
			},
			"additionalProperties": false,
		},
		Strict: false,
		Source: "gmail",
	}
}

// Execute turns the dedicated filter fields into a Gmail search query, lists
// matching message IDs, and fetches metadata for each result. Gmail's list
// endpoint returns only IDs, so the second step provides useful labels without
// reading the complete message body or attachments.
func (searchMessagesTool *SearchMessagesTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Subject    string `json:"subject"`
		Body       string `json:"body"`
		MaxResults *int   `json:"max_results"`
		PageToken  string `json:"page_token"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", searchGmailMessagesToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", searchGmailMessagesToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", searchGmailMessagesToolName, err)
	}

	query, err := buildMessageSearchQuery(arguments.From, arguments.To, arguments.Subject, arguments.Body)
	if err != nil {
		return "", err
	}
	if query == "" {
		return "", fmt.Errorf("at least one of from, to, subject, or body is required")
	}

	resultCount := defaultSearchResultCount
	if arguments.MaxResults != nil {
		resultCount = *arguments.MaxResults
		if resultCount < 1 || resultCount > maximumSearchResultCount {
			return "", fmt.Errorf("max_results must be between 1 and %d", maximumSearchResultCount)
		}
	}
	pageToken, err := validateSearchPageToken(arguments.PageToken)
	if err != nil {
		return "", err
	}

	queryValues := url.Values{
		"q":                {query},
		"maxResults":       {fmt.Sprintf("%d", resultCount)},
		"includeSpamTrash": {"false"},
	}
	if pageToken != "" {
		queryValues.Set("pageToken", pageToken)
	}
	responseBody, err := searchMessagesTool.client.doJSON(ctx, http.MethodGet, "/gmail/v1/users/me/messages?"+queryValues.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("search Gmail messages: %w", err)
	}

	var listResponse gmailMessageListResponse
	if err := json.Unmarshal(responseBody, &listResponse); err != nil {
		return "", fmt.Errorf("decode Gmail message search response: %w", err)
	}
	results := make([]gmailMessageSearchResult, 0, len(listResponse.Messages))
	for _, messageReference := range listResponse.Messages {
		messageID := strings.TrimSpace(messageReference.ID)
		if !gmailDraftIDPattern.MatchString(messageID) {
			return "", fmt.Errorf("Gmail message search response included an invalid message ID")
		}
		message, err := searchMessagesTool.messageMetadata(ctx, messageID)
		if err != nil {
			return "", err
		}
		if message.ID != messageID {
			return "", fmt.Errorf("Gmail message metadata response ID did not match the search result")
		}
		results = append(results, compactMessageSearchResult(message))
	}

	result := gmailMessageSearchResponse{
		Query:          query,
		Count:          len(results),
		EstimatedTotal: listResponse.ResultSizeEstimate,
		NextPageToken:  strings.TrimSpace(listResponse.NextPageToken),
		Messages:       results,
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode %s result: %w", searchGmailMessagesToolName, err)
	}
	return string(encodedResult), nil
}

func (searchMessagesTool *SearchMessagesTool) messageMetadata(ctx context.Context, messageID string) (gmailMessageMetadata, error) {
	queryValues := url.Values{
		"format":          {"metadata"},
		"metadataHeaders": {"From", "To", "Subject", "Date"},
	}
	responseBody, err := searchMessagesTool.client.doJSON(ctx, http.MethodGet, "/gmail/v1/users/me/messages/"+messageID+"?"+queryValues.Encode(), nil)
	if err != nil {
		return gmailMessageMetadata{}, fmt.Errorf("read Gmail message metadata: %w", err)
	}
	var message gmailMessageMetadata
	if err := json.Unmarshal(responseBody, &message); err != nil {
		return gmailMessageMetadata{}, fmt.Errorf("decode Gmail message metadata response: %w", err)
	}
	message.ID = strings.TrimSpace(message.ID)
	return message, nil
}

// buildMessageSearchQuery creates a single AND query. Values are always
// quoted literals, preventing special Gmail operators in one field from
// changing the meaning of another filter.
func buildMessageSearchQuery(from string, to string, subject string, body string) (string, error) {
	filters := []struct {
		name   string
		prefix string
		value  string
	}{
		{name: "from", prefix: "from:", value: from},
		{name: "to", prefix: "to:", value: to},
		{name: "subject", prefix: "subject:", value: subject},
		{name: "body", value: body},
	}
	queryParts := make([]string, 0, len(filters))
	for _, filter := range filters {
		term, err := normalizeMessageSearchTerm(filter.name, filter.value)
		if err != nil {
			return "", err
		}
		if term != "" {
			queryParts = append(queryParts, filter.prefix+quoteGmailSearchTerm(term))
		}
	}
	return strings.Join(queryParts, " "), nil
}

func normalizeMessageSearchTerm(fieldName string, rawTerm string) (string, error) {
	term := strings.TrimSpace(rawTerm)
	if term == "" {
		return "", nil
	}
	if !utf8.ValidString(term) {
		return "", fmt.Errorf("%s must be valid UTF-8", fieldName)
	}
	if utf8.RuneCountInString(term) > maximumSearchTermLength {
		return "", fmt.Errorf("%s must not exceed %d characters", fieldName, maximumSearchTermLength)
	}
	if strings.IndexFunc(term, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s must not contain control characters", fieldName)
	}
	return term, nil
}

func quoteGmailSearchTerm(term string) string {
	escapedTerm := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(term)
	return `"` + escapedTerm + `"`
}

func validateSearchPageToken(rawPageToken string) (string, error) {
	pageToken := strings.TrimSpace(rawPageToken)
	if pageToken == "" {
		return "", nil
	}
	if !utf8.ValidString(pageToken) || utf8.RuneCountInString(pageToken) > maximumSearchPageTokenSize || strings.IndexFunc(pageToken, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("page_token is invalid")
	}
	return pageToken, nil
}

type gmailMessageListResponse struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	NextPageToken      string `json:"nextPageToken"`
	ResultSizeEstimate int    `json:"resultSizeEstimate"`
}

type gmailMessageMetadata struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
	Snippet  string `json:"snippet"`
	Payload  struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	} `json:"payload"`
}

type gmailMessageSearchResult struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Date      string `json:"date,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
}

type gmailMessageSearchResponse struct {
	Query          string                     `json:"query"`
	Count          int                        `json:"count"`
	EstimatedTotal int                        `json:"estimated_total"`
	NextPageToken  string                     `json:"next_page_token,omitempty"`
	Messages       []gmailMessageSearchResult `json:"messages"`
}

func compactMessageSearchResult(message gmailMessageMetadata) gmailMessageSearchResult {
	result := gmailMessageSearchResult{
		MessageID: message.ID,
		ThreadID:  strings.TrimSpace(message.ThreadID),
		Snippet:   truncateSearchText(message.Snippet, maximumSearchSnippetLength),
	}
	for _, header := range message.Payload.Headers {
		switch strings.ToLower(strings.TrimSpace(header.Name)) {
		case "from":
			result.From = strings.TrimSpace(header.Value)
		case "to":
			result.To = strings.TrimSpace(header.Value)
		case "subject":
			result.Subject = strings.TrimSpace(header.Value)
		case "date":
			result.Date = strings.TrimSpace(header.Value)
		}
	}
	return result
}

func truncateSearchText(rawText string, maximumLength int) string {
	normalizedText := strings.TrimSpace(strings.Join(strings.Fields(rawText), " "))
	if len(normalizedText) <= maximumLength {
		return normalizedText
	}
	truncatedText := normalizedText[:maximumLength]
	for len(truncatedText) > 0 && !utf8.ValidString(truncatedText) {
		truncatedText = truncatedText[:len(truncatedText)-1]
	}
	return strings.TrimSpace(truncatedText) + "…"
}
