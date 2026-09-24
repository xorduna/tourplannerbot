package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strings"

	"tourplannerbot/internal/tools"
)

const createEmailDraftToolName = "create_bigin_email_draft"

// CreateEmailDraftTool creates an unsent email draft associated with one Bigin
// deal. It has no code path or API endpoint capable of sending email.
type CreateEmailDraftTool struct {
	client *Client
}

// NewCreateEmailDraft creates the Bigin email-draft tool.
func NewCreateEmailDraft(client *Client) (*CreateEmailDraftTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	return &CreateEmailDraftTool{client: client}, nil
}

// Definition describes the create_bigin_email_draft function to the LLM.
func (createEmailDraftTool *CreateEmailDraftTool) Definition() tools.Definition {
	return tools.Definition{
		Name: createEmailDraftToolName,
		Description: "Create an unsent plain-text email draft associated with a Zoho Bigin deal. This only saves a draft for manual review and sending in the Bigin UI; it never sends email. " +
			"Use only after the user has clearly requested creation of the draft and the deal, sender, recipient, subject, and complete body are known.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID to associate with the draft.",
				},
				"from_email": map[string]any{
					"type":        "string",
					"description": "The configured Bigin sender email address that will appear in the draft.",
				},
				"to_email": map[string]any{
					"type":        "string",
					"description": "The recipient email address to place in the unsent draft.",
				},
				"to_name": map[string]any{
					"type":        "string",
					"description": "Recipient display name, or an empty string when unknown.",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "The complete email subject.",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "The complete plain-text email body to save as a draft.",
				},
			},
			"required":             []string{"deal_id", "from_email", "to_email", "to_name", "subject", "body"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute validates and creates one unsent plain-text draft through the CRM v8
// endpoint used by Bigin. No send-mail endpoint is present in this package.
func (createEmailDraftTool *CreateEmailDraftTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID    string `json:"deal_id"`
		FromEmail string `json:"from_email"`
		ToEmail   string `json:"to_email"`
		ToName    string `json:"to_name"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", createEmailDraftToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", createEmailDraftToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", createEmailDraftToolName, err)
	}

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	fromEmail, err := normalizedEmailAddress(arguments.FromEmail, "from_email")
	if err != nil {
		return "", err
	}
	toEmail, err := normalizedEmailAddress(arguments.ToEmail, "to_email")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(arguments.Body) == "" {
		return "", fmt.Errorf("body must not be empty")
	}

	requestPayload := struct {
		EmailDrafts []emailDraftPayload `json:"__email_drafts"`
	}{
		EmailDrafts: []emailDraftPayload{
			{
				From: fromEmail,
				To: []emailDraftRecipient{
					{UserName: strings.TrimSpace(arguments.ToName), Email: toEmail},
				},
				Subject:  strings.TrimSpace(arguments.Subject),
				Content:  arguments.Body,
				RichText: false,
			},
		},
	}
	encodedPayload, err := json.Marshal(requestPayload)
	if err != nil {
		return "", fmt.Errorf("encode Bigin email draft: %w", err)
	}

	responseBody, err := createEmailDraftTool.client.postJSON(ctx, "/crm/v8/Deals/"+dealID+"/__email_drafts", encodedPayload)
	if err != nil {
		return "", fmt.Errorf("create Bigin email draft: %w", err)
	}
	if err := validateCreateEmailDraftResponse(responseBody); err != nil {
		return "", err
	}
	return string(responseBody), nil
}

type emailDraftPayload struct {
	From     string                `json:"from"`
	To       []emailDraftRecipient `json:"to"`
	Subject  string                `json:"subject"`
	Content  string                `json:"content"`
	RichText bool                  `json:"rich_text"`
}

type emailDraftRecipient struct {
	UserName string `json:"user_name"`
	Email    string `json:"email"`
}

// normalizedEmailAddress validates one mailbox and returns only its address.
func normalizedEmailAddress(rawEmailAddress string, fieldName string) (string, error) {
	parsedAddress, err := mail.ParseAddress(strings.TrimSpace(rawEmailAddress))
	if err != nil || parsedAddress.Address == "" {
		return "", fmt.Errorf("%s must be a valid email address", fieldName)
	}
	return parsedAddress.Address, nil
}

// validateCreateEmailDraftResponse treats application-level error items in a
// successful HTTP response as tool errors instead of claiming draft creation.
func validateCreateEmailDraftResponse(responseBody []byte) error {
	responsePayload := struct {
		EmailDrafts []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"__email_drafts"`
	}{}
	if err := json.Unmarshal(responseBody, &responsePayload); err != nil {
		return fmt.Errorf("decode Bigin email draft response: %w", err)
	}
	if len(responsePayload.EmailDrafts) != 1 {
		return fmt.Errorf("Bigin email draft response did not contain exactly one result")
	}
	result := responsePayload.EmailDrafts[0]
	if !strings.EqualFold(result.Status, "success") {
		if result.Code != "" && result.Message != "" {
			return fmt.Errorf("Bigin email draft creation failed (%s): %s", result.Code, result.Message)
		}
		return fmt.Errorf("Bigin email draft creation failed")
	}
	return nil
}
