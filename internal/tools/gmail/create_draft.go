package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	createGmailDraftToolName = "create_gmail_draft"
	maximumRecipientCount    = 100
	maximumSubjectLength     = 998
	maximumBodyBytes         = 1 << 20
)

// CreateDraftTool creates unsent plain-text drafts in the configured Gmail
// account. Sending mail is deliberately outside this tool's responsibility.
type CreateDraftTool struct {
	client *Client
}

// NewCreateDraft creates the Gmail draft creation tool.
func NewCreateDraft(client *Client) (*CreateDraftTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Gmail client is required")
	}
	return &CreateDraftTool{client: client}, nil
}

// Definition describes create_gmail_draft to the LLM. Its distinct name avoids
// colliding with create_draft, which creates an editable in-app proposal.
func (createDraftTool *CreateDraftTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        createGmailDraftToolName,
		Description: "Create an unsent plain-text draft in the configured Gmail account. Use this only when the user explicitly asks to create or save an email draft in Gmail. This tool never sends the email.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"minItems":    1,
					"description": "Primary recipient email addresses. Each item may also use the form Name <email@example.com>.",
				},
				"cc": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional carbon-copy recipient email addresses.",
				},
				"bcc": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional blind-carbon-copy recipient email addresses.",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Complete email subject.",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Complete plain-text email body.",
				},
			},
			"required":             []string{"to", "cc", "bcc", "subject", "body"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "gmail",
	}
}

// Execute validates recipients, constructs an RFC 2822 MIME message, encodes
// it as base64url, and creates an unsent draft through the Gmail API.
func (createDraftTool *CreateDraftTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		To      []string `json:"to"`
		Cc      []string `json:"cc"`
		Bcc     []string `json:"bcc"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", createGmailDraftToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", createGmailDraftToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", createGmailDraftToolName, err)
	}

	requestBody, err := buildDraftRequest(arguments.To, arguments.Cc, arguments.Bcc, arguments.Subject, arguments.Body)
	if err != nil {
		return "", err
	}

	responseBody, err := createDraftTool.client.doJSON(ctx, http.MethodPost, "/gmail/v1/users/me/drafts", requestBody)
	if err != nil {
		return "", fmt.Errorf("create Gmail draft: %w", err)
	}
	return encodeDraftResult(responseBody, "draft_created", "")
}

// buildDraftRequest validates a complete message and wraps its base64url MIME
// representation in the Gmail Draft resource request shape.
func buildDraftRequest(to []string, carbonCopies []string, blindCarbonCopies []string, subject string, body string) (any, error) {
	toRecipients, err := normalizeRecipients(to, "to")
	if err != nil {
		return nil, err
	}
	if len(toRecipients) == 0 {
		return nil, fmt.Errorf("to must contain at least one recipient")
	}
	carbonCopyRecipients, err := normalizeRecipients(carbonCopies, "cc")
	if err != nil {
		return nil, err
	}
	blindCarbonCopyRecipients, err := normalizeRecipients(blindCarbonCopies, "bcc")
	if err != nil {
		return nil, err
	}
	if len(toRecipients)+len(carbonCopyRecipients)+len(blindCarbonCopyRecipients) > maximumRecipientCount {
		return nil, fmt.Errorf("recipient count must not exceed %d", maximumRecipientCount)
	}
	if !utf8.ValidString(subject) {
		return nil, fmt.Errorf("subject must be valid UTF-8")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return nil, fmt.Errorf("subject must not contain line breaks")
	}
	if utf8.RuneCountInString(subject) > maximumSubjectLength {
		return nil, fmt.Errorf("subject must not exceed %d characters", maximumSubjectLength)
	}
	if !utf8.ValidString(body) {
		return nil, fmt.Errorf("body must be valid UTF-8")
	}
	if len(body) > maximumBodyBytes {
		return nil, fmt.Errorf("body must not exceed %d bytes", maximumBodyBytes)
	}

	rawMessage := buildPlainTextMessage(toRecipients, carbonCopyRecipients, blindCarbonCopyRecipients, subject, body)
	requestBody := struct {
		Message struct {
			Raw string `json:"raw"`
		} `json:"message"`
	}{}
	requestBody.Message.Raw = base64.RawURLEncoding.EncodeToString(rawMessage)
	return requestBody, nil
}

// encodeDraftResult validates Gmail's response and returns only stable resource
// identifiers and the requested operation status to the model.
func encodeDraftResult(responseBody []byte, status string, expectedDraftID string) (string, error) {
	gmailDraft := struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}{}
	if err := json.Unmarshal(responseBody, &gmailDraft); err != nil {
		return "", fmt.Errorf("decode Gmail draft response: %w", err)
	}
	if strings.TrimSpace(gmailDraft.ID) == "" || strings.TrimSpace(gmailDraft.Message.ID) == "" {
		return "", fmt.Errorf("Gmail draft response did not include draft and message IDs")
	}
	if expectedDraftID != "" && gmailDraft.ID != expectedDraftID {
		return "", fmt.Errorf("Gmail draft response ID did not match the updated draft")
	}

	result := struct {
		DraftID   string `json:"draft_id"`
		MessageID string `json:"message_id"`
		ThreadID  string `json:"thread_id,omitempty"`
		Status    string `json:"status"`
	}{
		DraftID:   gmailDraft.ID,
		MessageID: gmailDraft.Message.ID,
		ThreadID:  gmailDraft.Message.ThreadID,
		Status:    status,
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode Gmail draft result: %w", err)
	}
	return string(encodedResult), nil
}

// normalizeRecipients parses and canonicalizes recipient mailboxes. Parsing
// through net/mail also rejects CRLF header injection attempts.
func normalizeRecipients(rawRecipients []string, fieldName string) ([]string, error) {
	normalizedRecipients := make([]string, 0, len(rawRecipients))
	for recipientIndex, rawRecipient := range rawRecipients {
		trimmedRecipient := strings.TrimSpace(rawRecipient)
		if trimmedRecipient == "" {
			return nil, fmt.Errorf("%s recipient %d must not be empty", fieldName, recipientIndex+1)
		}
		parsedRecipient, err := mail.ParseAddress(trimmedRecipient)
		if err != nil {
			return nil, fmt.Errorf("%s recipient %d must be a valid email address: %w", fieldName, recipientIndex+1, err)
		}
		normalizedRecipients = append(normalizedRecipients, parsedRecipient.String())
	}
	return normalizedRecipients, nil
}

// buildPlainTextMessage returns a CRLF-normalized MIME message suitable for
// Gmail's message.raw field.
func buildPlainTextMessage(toRecipients []string, carbonCopyRecipients []string, blindCarbonCopyRecipients []string, subject string, body string) []byte {
	var messageBuilder strings.Builder
	messageBuilder.WriteString("To: ")
	messageBuilder.WriteString(strings.Join(toRecipients, ", "))
	messageBuilder.WriteString("\r\n")
	if len(carbonCopyRecipients) > 0 {
		messageBuilder.WriteString("Cc: ")
		messageBuilder.WriteString(strings.Join(carbonCopyRecipients, ", "))
		messageBuilder.WriteString("\r\n")
	}
	if len(blindCarbonCopyRecipients) > 0 {
		messageBuilder.WriteString("Bcc: ")
		messageBuilder.WriteString(strings.Join(blindCarbonCopyRecipients, ", "))
		messageBuilder.WriteString("\r\n")
	}
	messageBuilder.WriteString("Subject: ")
	messageBuilder.WriteString(mime.QEncoding.Encode("UTF-8", subject))
	messageBuilder.WriteString("\r\n")
	messageBuilder.WriteString("MIME-Version: 1.0\r\n")
	messageBuilder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	messageBuilder.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	normalizedBody := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	messageBuilder.WriteString(strings.ReplaceAll(normalizedBody, "\n", "\r\n"))
	return []byte(messageBuilder.String())
}
