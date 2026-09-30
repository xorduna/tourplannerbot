package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"tourplannerbot/internal/tools"
)

const (
	getDealEmailsToolName = "get_bigin_deal_emails"
	maximumEmailMessageID = 512
)

var biginEmailMessageIDPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

// GetDealEmailsTool retrieves the email related list for one Bigin pipeline
// record, or the full detail of an email returned by that list.
type GetDealEmailsTool struct {
	client *Client
}

// NewGetDealEmails creates the read-only Bigin email lookup tool.
func NewGetDealEmails(client *Client) (*GetDealEmailsTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	return &GetDealEmailsTool{client: client}, nil
}

// Definition describes get_bigin_deal_emails to the LLM. A detail request
// deliberately requires a message ID from a previous list response so callers
// inspect the email list before retrieving an email body.
func (getDealEmailsTool *GetDealEmailsTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        getDealEmailsToolName,
		Description: "Retrieve emails related to one Zoho Bigin deal (pipeline record). Call without message_id to list email summaries. Use a message_id returned by that list to retrieve one email's full detail, including its body, only when the user asks to read it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID.",
				},
				"message_id": map[string]any{
					"type":        "string",
					"description": "Optional email message ID returned by an earlier list call. Omit it to list related emails.",
				},
			},
			"required":             []string{"deal_id"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute validates opaque record and message identifiers, then forwards the
// untouched Bigin JSON envelope. It never changes the email or pipeline data.
func (getDealEmailsTool *GetDealEmailsTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID    string `json:"deal_id"`
		MessageID string `json:"message_id"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", getDealEmailsToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", getDealEmailsToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", getDealEmailsToolName, err)
	}

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}

	apiPath := "/bigin/v2/Pipelines/" + dealID + "/Emails"
	messageID := strings.TrimSpace(arguments.MessageID)
	if messageID != "" {
		if len(messageID) > maximumEmailMessageID || !biginEmailMessageIDPattern.MatchString(messageID) {
			return "", fmt.Errorf("message_id must contain at most %d letters, digits, dots, at signs, hyphens, or underscores", maximumEmailMessageID)
		}
		apiPath += "/" + url.PathEscape(messageID)
	}

	responseBody, err := getDealEmailsTool.client.get(ctx, apiPath)
	if err != nil {
		return "", fmt.Errorf("retrieve Bigin deal emails: %w", err)
	}
	return string(responseBody), nil
}
