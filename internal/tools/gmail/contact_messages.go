package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
)

// ContactMessage is a compact Gmail message that can be presented as a reply
// target. MessageID and ThreadID are Gmail identifiers, not CRM identifiers.
type ContactMessage struct {
	MessageID string
	ThreadID  string
	From      string
	To        string
	Subject   string
	Date      string
	Snippet   string
}

// SearchContactMessages returns at most one recent message per Gmail thread
// exchanged with emailAddress. It is deliberately scoped to a known CRM
// contact and does not accept arbitrary Gmail query syntax.
func (client *Client) SearchContactMessages(ctx context.Context, emailAddress string) ([]ContactMessage, error) {
	parsedAddress, err := mail.ParseAddress(strings.TrimSpace(emailAddress))
	if err != nil || parsedAddress.Address == "" {
		return nil, fmt.Errorf("contact email is invalid")
	}
	address := parsedAddress.Address
	query := "from:" + quoteGmailSearchTerm(address) + " OR to:" + quoteGmailSearchTerm(address)
	queryValues := url.Values{
		"q":                {query},
		"maxResults":       {fmt.Sprintf("%d", maximumSearchResultCount)},
		"includeSpamTrash": {"false"},
	}
	responseBody, err := client.doJSON(ctx, http.MethodGet, "/gmail/v1/users/me/messages?"+queryValues.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("search Gmail contact messages: %w", err)
	}
	var listResponse gmailMessageListResponse
	if err := json.Unmarshal(responseBody, &listResponse); err != nil {
		return nil, fmt.Errorf("decode Gmail contact search: %w", err)
	}
	messages := make([]ContactMessage, 0, len(listResponse.Messages))
	seenThreads := make(map[string]struct{})
	searchTool := &SearchMessagesTool{client: client}
	for _, messageReference := range listResponse.Messages {
		messageID := strings.TrimSpace(messageReference.ID)
		if !gmailDraftIDPattern.MatchString(messageID) {
			return nil, fmt.Errorf("Gmail contact search included an invalid message ID")
		}
		metadata, err := searchTool.messageMetadata(ctx, messageID)
		if err != nil {
			return nil, err
		}
		compact := compactMessageSearchResult(metadata)
		if compact.ThreadID == "" {
			continue
		}
		if _, seen := seenThreads[compact.ThreadID]; seen {
			continue
		}
		seenThreads[compact.ThreadID] = struct{}{}
		messages = append(messages, ContactMessage{
			MessageID: compact.MessageID,
			ThreadID:  compact.ThreadID,
			From:      compact.From,
			To:        compact.To,
			Subject:   compact.Subject,
			Date:      compact.Date,
			Snippet:   compact.Snippet,
		})
	}
	return messages, nil
}
