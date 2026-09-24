package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"tourplannerbot/internal/tools"
)

const updateGmailDraftToolName = "update_gmail_draft"

var gmailDraftIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// UpdateDraftTool replaces the complete MIME message inside an existing Gmail
// draft. Gmail preserves the draft ID while assigning the replacement message
// a new message ID.
type UpdateDraftTool struct {
	client *Client
}

// NewUpdateDraft creates the Gmail draft replacement tool.
func NewUpdateDraft(client *Client) (*UpdateDraftTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Gmail client is required")
	}
	return &UpdateDraftTool{client: client}, nil
}

// Definition describes update_gmail_draft to the LLM. Every content field is
// required because Gmail replaces rather than partially patches the message.
func (updateDraftTool *UpdateDraftTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        updateGmailDraftToolName,
		Description: "Replace the complete content of an existing unsent Gmail draft. Use the draft_id returned by create_gmail_draft and provide the complete recipient, subject, and body values. This tool never sends the email.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"draft_id": map[string]any{
					"type":        "string",
					"description": "The Gmail draft ID returned by create_gmail_draft.",
				},
				"to": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"minItems":    1,
					"description": "Complete list of primary recipient email addresses.",
				},
				"cc": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Complete list of carbon-copy recipient email addresses, or an empty array.",
				},
				"bcc": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Complete list of blind-carbon-copy recipient email addresses, or an empty array.",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Complete replacement email subject.",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Complete replacement plain-text email body.",
				},
			},
			"required":             []string{"draft_id", "to", "cc", "bcc", "subject", "body"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "gmail",
	}
}

// Execute validates the complete replacement message and updates the Gmail
// draft selected by its opaque resource ID.
func (updateDraftTool *UpdateDraftTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DraftID string   `json:"draft_id"`
		To      []string `json:"to"`
		Cc      []string `json:"cc"`
		Bcc     []string `json:"bcc"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", updateGmailDraftToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", updateGmailDraftToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", updateGmailDraftToolName, err)
	}

	draftID := strings.TrimSpace(arguments.DraftID)
	if !gmailDraftIDPattern.MatchString(draftID) {
		return "", fmt.Errorf("draft_id must contain only letters, digits, hyphens, or underscores")
	}
	requestBody, err := buildDraftRequest(arguments.To, arguments.Cc, arguments.Bcc, arguments.Subject, arguments.Body)
	if err != nil {
		return "", err
	}

	responseBody, err := updateDraftTool.client.doJSON(ctx, http.MethodPut, "/gmail/v1/users/me/drafts/"+draftID, requestBody)
	if err != nil {
		return "", fmt.Errorf("update Gmail draft: %w", err)
	}
	return encodeDraftResult(responseBody, "draft_updated", draftID)
}
