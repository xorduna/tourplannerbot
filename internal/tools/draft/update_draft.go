package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tourplannerbot/internal/database"
	"tourplannerbot/internal/models"
	applicationTools "tourplannerbot/internal/tools"

	"gorm.io/gorm"
)

const updateDraftToolName = "update_draft"

// UpdateTool replaces the active draft using its trusted ID and owner.
type UpdateTool struct {
	databaseConnection *gorm.DB
}

// NewUpdate creates the native active-draft update tool.
func NewUpdate(databaseConnection *gorm.DB) (*UpdateTool, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	return &UpdateTool{databaseConnection: databaseConnection}, nil
}

// Definition describes the model-facing update contract. The draft ID and
// owner are deliberately absent because they are trusted execution context.
func (tool *UpdateTool) Definition() applicationTools.Definition {
	return applicationTools.Definition{
		Name:        updateDraftToolName,
		Description: "Replace the current active draft after Diana explicitly asks to change it. Use the active draft revision supplied in the conversation context. Send the complete replacement body in basic Markdown; do not use this to create a new text. The current subject is preserved.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"body": map[string]any{
					"type":        "string",
					"description": "The complete updated draft body, using basic Markdown for bold, italic, lists, and HTTPS links when useful.",
				},
				"expected_revision": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"description": "The revision of the active draft supplied in context.",
				},
			},
			"required":             []string{"body", "expected_revision"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "native",
	}
}

// Execute validates the replacement body, converts its Markdown to the
// canonical document, and uses the shared optimistic update transaction.
func (tool *UpdateTool) Execute(applicationContext context.Context, rawArguments json.RawMessage) (string, error) {
	toolExecutionContext, found := applicationTools.ExecutionContextFromContext(applicationContext)
	if !found {
		return "", errors.New("trusted tool execution context is required")
	}
	activeDraft := toolExecutionContext.ActiveDraft()
	if activeDraft == nil {
		return "", errors.New("there is no active draft to update")
	}
	if toolExecutionContext.UpdatedDraft() != nil {
		return "", errors.New("the active draft has already been updated for this response")
	}

	arguments := struct {
		Body             string `json:"body"`
		ExpectedRevision int    `json:"expected_revision"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode update_draft arguments: %w", err)
	}
	if err := ensureJSONDocumentEnded(decoder); err != nil {
		return "", err
	}
	if arguments.ExpectedRevision < 1 {
		return "", errors.New("expected draft revision must be positive")
	}
	if strings.TrimSpace(arguments.Body) == "" {
		return "", errors.New("draft body is required")
	}
	contentJSON, err := models.NewTiptapDocumentFromMarkdown(arguments.Body)
	if err != nil {
		return "", fmt.Errorf("convert update_draft Markdown: %w", err)
	}
	bodyText, err := models.ValidateAndProjectTiptapDocument(contentJSON)
	if err != nil {
		return "", fmt.Errorf("project update_draft content: %w", err)
	}
	updatedDraft, err := database.UpdateDraft(applicationContext, tool.databaseConnection, database.UpdateDraftInput{
		ID:               activeDraft.ID,
		OwnerTelegramID:  toolExecutionContext.OwnerTelegramID,
		ExpectedRevision: arguments.ExpectedRevision,
		Subject:          activeDraft.Subject,
		ContentJSON:      contentJSON,
		BodyText:         bodyText,
	})
	if err != nil {
		return "", fmt.Errorf("update draft: %w", err)
	}
	toolExecutionContext.RecordUpdatedDraft(updatedDraft)

	encodedResult, err := json.Marshal(newCanonicalDraftResult(updatedDraft))
	if err != nil {
		return "", fmt.Errorf("encode update_draft result: %w", err)
	}
	return string(encodedResult), nil
}
