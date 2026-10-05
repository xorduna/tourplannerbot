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

// DraftInput is the complete message used to create an unsent Gmail draft.
// Reply fields are populated only by CreateReplyDraft after reading a trusted
// message from Gmail itself.
type DraftInput struct {
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
}

// DraftResult identifies a newly created Gmail draft.
type DraftResult struct {
	DraftID   string
	MessageID string
	ThreadID  string
}

// CreateDraft creates a new, standalone Gmail draft. It is intentionally a
// small application-facing API rather than an LLM tool contract.
func (client *Client) CreateDraft(ctx context.Context, input DraftInput) (DraftResult, error) {
	return client.createDraft(ctx, input, "", "", "")
}

// CreateReplyDraft creates an unsent reply to one existing Gmail message.
// The message ID comes from Bigin's deal-related-email list, but all threading
// metadata is re-read from Gmail so a Bigin identifier can never forge a
// thread or MIME header.
func (client *Client) CreateReplyDraft(ctx context.Context, rawMessageID string, body string) (DraftResult, error) {
	messageID := strings.TrimSpace(rawMessageID)
	if !gmailDraftIDPattern.MatchString(messageID) {
		return DraftResult{}, fmt.Errorf("message_id is invalid")
	}
	metadata, err := client.replyMetadata(ctx, messageID)
	if err != nil {
		return DraftResult{}, err
	}
	if metadata.ID != messageID || !gmailDraftIDPattern.MatchString(metadata.ThreadID) {
		return DraftResult{}, fmt.Errorf("Gmail message did not include a valid thread")
	}
	targetMessageID := metadata.header("Message-ID")
	subject := metadata.header("Subject")
	recipientHeader := metadata.header("Reply-To")
	if metadata.hasLabel("SENT") {
		recipientHeader = metadata.header("To")
	} else if recipientHeader == "" {
		recipientHeader = metadata.header("From")
	}
	recipients, err := parseReplyRecipients(recipientHeader)
	if !validReplyHeader(targetMessageID) || strings.TrimSpace(subject) == "" || err != nil || len(recipients) == 0 {
		return DraftResult{}, fmt.Errorf("Gmail message cannot be used as a reply target")
	}
	references := strings.TrimSpace(metadata.header("References"))
	if references != "" {
		references += " "
	}
	references += targetMessageID
	if !validReplyHeader(references) {
		return DraftResult{}, fmt.Errorf("Gmail message has invalid reply references")
	}
	return client.createDraft(ctx, DraftInput{To: recipients, Subject: subject, Body: body}, metadata.ThreadID, targetMessageID, references)
}

func (client *Client) createDraft(ctx context.Context, input DraftInput, threadID string, inReplyTo string, references string) (DraftResult, error) {
	requestBody, err := buildDraftRequestWithReply(input.To, input.Cc, input.Bcc, input.Subject, input.Body, threadID, inReplyTo, references)
	if err != nil {
		return DraftResult{}, err
	}
	responseBody, err := client.doJSON(ctx, http.MethodPost, "/gmail/v1/users/me/drafts", requestBody)
	if err != nil {
		return DraftResult{}, fmt.Errorf("create Gmail draft: %w", err)
	}
	var response struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return DraftResult{}, fmt.Errorf("decode Gmail draft response: %w", err)
	}
	if strings.TrimSpace(response.ID) == "" || strings.TrimSpace(response.Message.ID) == "" {
		return DraftResult{}, fmt.Errorf("Gmail draft response did not include draft and message IDs")
	}
	return DraftResult{DraftID: response.ID, MessageID: response.Message.ID, ThreadID: response.Message.ThreadID}, nil
}

type replyMessageMetadata struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"threadId"`
	LabelIDs []string `json:"labelIds"`
	Payload  struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	} `json:"payload"`
}

func (client *Client) replyMetadata(ctx context.Context, messageID string) (replyMessageMetadata, error) {
	query := url.Values{"format": {"metadata"}}
	for _, header := range []string{"Message-ID", "References", "In-Reply-To", "Reply-To", "From", "To", "Subject"} {
		query.Add("metadataHeaders", header)
	}
	responseBody, err := client.doJSON(ctx, http.MethodGet, "/gmail/v1/users/me/messages/"+messageID+"?"+query.Encode(), nil)
	if err != nil {
		return replyMessageMetadata{}, fmt.Errorf("read Gmail reply target: %w", err)
	}
	var metadata replyMessageMetadata
	if err := json.Unmarshal(responseBody, &metadata); err != nil {
		return replyMessageMetadata{}, fmt.Errorf("decode Gmail reply target: %w", err)
	}
	metadata.ID = strings.TrimSpace(metadata.ID)
	metadata.ThreadID = strings.TrimSpace(metadata.ThreadID)
	return metadata, nil
}

func (metadata replyMessageMetadata) header(name string) string {
	for _, header := range metadata.Payload.Headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), name) {
			return strings.TrimSpace(header.Value)
		}
	}
	return ""
}

func (metadata replyMessageMetadata) hasLabel(expectedLabel string) bool {
	for _, label := range metadata.LabelIDs {
		if strings.EqualFold(strings.TrimSpace(label), expectedLabel) {
			return true
		}
	}
	return false
}

func parseReplyRecipients(rawRecipients string) ([]string, error) {
	parsedRecipients, err := mail.ParseAddressList(strings.TrimSpace(rawRecipients))
	if err != nil {
		return nil, err
	}
	recipients := make([]string, 0, len(parsedRecipients))
	for _, recipient := range parsedRecipients {
		recipients = append(recipients, recipient.String())
	}
	return recipients, nil
}

func validReplyHeader(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\r\n") && len(value) <= maximumSubjectLength
}
