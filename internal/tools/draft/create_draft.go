// Package draft implements native tools for creating collaborative drafts.
package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"tourplannerbot/internal/database"
	"tourplannerbot/internal/models"
	applicationTools "tourplannerbot/internal/tools"

	"gorm.io/gorm"
)

const createDraftToolName = "create_draft"

type canonicalDraftResult struct {
	ID       string           `json:"id"`
	Kind     models.DraftKind `json:"kind"`
	Subject  *string          `json:"subject"`
	Body     string           `json:"body"`
	Revision int              `json:"revision"`
}

// Tool creates a revision-one draft using only trusted execution context.
type Tool struct {
	databaseConnection *gorm.DB
}

// New creates the native draft tool backed by the supplied database.
func New(databaseConnection *gorm.DB) (*Tool, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	return &Tool{databaseConnection: databaseConnection}, nil
}

// Definition describes the restricted function contract exposed to the model.
// Conversation, owner, revision, and draft status are intentionally absent.
func (tool *Tool) Definition() applicationTools.Definition {
	return applicationTools.Definition{
		Name:        createDraftToolName,
		Description: "Create a new editable email, WhatsApp, or generic draft when Diana explicitly asks you to write or reply with a sendable text. Use basic Markdown in body for bold (**text**), italic (*text*), lists, and HTTPS links. Call this exactly once for the new draft, then use its canonical result when replying.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"enum":        []string{string(models.DraftKindEmail), string(models.DraftKindWhatsApp), string(models.DraftKindGeneric)},
					"description": "The communication format of the draft.",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "The complete text that Diana can edit and send, using basic Markdown for bold, italic, lists, and HTTPS links when needed.",
				},
			},
			"required":             []string{"kind", "body"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "native",
	}
}

// Execute validates model-provided content, persists it with trusted identity,
// and returns the canonical draft JSON. It never accepts owner or conversation
// identifiers from model arguments.
func (tool *Tool) Execute(applicationContext context.Context, rawArguments json.RawMessage) (string, error) {
	toolExecutionContext, found := applicationTools.ExecutionContextFromContext(applicationContext)
	if !found {
		return "", errors.New("trusted tool execution context is required")
	}
	if toolExecutionContext.CreatedDraft() != nil {
		return "", errors.New("a draft has already been created for this response")
	}

	arguments := struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode create_draft arguments: %w", err)
	}
	if err := ensureJSONDocumentEnded(decoder); err != nil {
		return "", err
	}

	draftKind := models.DraftKind(arguments.Kind)
	if !draftKind.IsValid() {
		return "", fmt.Errorf("unsupported draft kind %q", arguments.Kind)
	}
	if strings.TrimSpace(arguments.Body) == "" {
		return "", errors.New("draft body is required")
	}

	contentJSON, err := models.NewTiptapDocumentFromMarkdown(arguments.Body)
	if err != nil {
		return "", fmt.Errorf("convert create_draft Markdown: %w", err)
	}
	createdDraft, err := database.CreateDraft(applicationContext, tool.databaseConnection, database.CreateDraftInput{
		ChatID:          toolExecutionContext.ChatID,
		MessageThreadID: toolExecutionContext.MessageThreadID,
		OwnerTelegramID: toolExecutionContext.OwnerTelegramID,
		Kind:            draftKind,
		ContentJSON:     contentJSON,
		BodyText:        arguments.Body,
	})
	if err != nil {
		return "", fmt.Errorf("create draft: %w", err)
	}
	toolExecutionContext.RecordCreatedDraft(createdDraft)

	encodedResult, err := json.Marshal(newCanonicalDraftResult(createdDraft))
	if err != nil {
		return "", fmt.Errorf("encode create_draft result: %w", err)
	}
	return string(encodedResult), nil
}

// canonicalDraftResult is the stable JSON representation returned to the
// model after either native draft mutation.
func newCanonicalDraftResult(draft *models.Draft) canonicalDraftResult {
	return canonicalDraftResult{
		ID:       draft.ID,
		Kind:     draft.Kind,
		Subject:  draft.Subject,
		Body:     draft.BodyText,
		Revision: draft.Revision,
	}
}

// ensureJSONDocumentEnded rejects trailing JSON values after the arguments object.
func ensureJSONDocumentEnded(decoder *json.Decoder) error {
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return errors.New("decode create_draft arguments: multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode create_draft arguments: %w", err)
	}
	return nil
}
