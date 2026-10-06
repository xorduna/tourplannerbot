// Package tools defines the provider-independent abstraction used by native and
// MCP-backed tools.
package tools

import (
	"context"
	"encoding/json"
	"errors"

	"tourplannerbot/internal/models"
)

// Definition describes a callable tool to an LLM.
type Definition struct {
	Name        string
	Description string
	Parameters  map[string]any
	Strict      bool
	Source      string
}

// Tool exposes a definition and executes one call using JSON arguments.
type Tool interface {
	Definition() Definition
	Execute(ctx context.Context, arguments json.RawMessage) (string, error)
}

type executionContextKey struct{}

// ExecutionContext contains trusted identity and conversation values available
// to native tools. These values are derived from the incoming Telegram update,
// never from arguments requested by the model.
type ExecutionContext struct {
	ChatID          int64
	MessageThreadID int
	OwnerTelegramID int64
	activeDraft     *models.Draft
	createdDraft    *models.Draft
	updatedDraft    *models.Draft
	downloadedFiles []DownloadedFile
}

// DownloadedFile is a temporary file prepared by a trusted native tool for
// delivery to the current Telegram conversation. Path and CleanupPath are
// intentionally absent from LLM tool results.
type DownloadedFile struct {
	Path        string
	Filename    string
	MIMEType    string
	Size        int64
	CleanupPath string
	// AnalysisOnly keeps a downloaded source file available to the active model
	// response without sending it to Telegram. The default false preserves
	// delivery behavior for tools whose explicit purpose is file delivery.
	AnalysisOnly bool
}

// SetActiveDraft makes the current authorized draft available to native tools
// for this one generation. The handler loads it from PostgreSQL immediately
// before invoking the model.
func (toolExecutionContext *ExecutionContext) SetActiveDraft(activeDraft *models.Draft) {
	toolExecutionContext.activeDraft = activeDraft
}

// ActiveDraft returns the active draft snapshot loaded before generation.
func (toolExecutionContext *ExecutionContext) ActiveDraft() *models.Draft {
	return toolExecutionContext.activeDraft
}

// NewExecutionContext validates and creates the trusted context for one model
// generation. A generation gets its own instance so tools cannot share state
// with another incoming Telegram message.
func NewExecutionContext(chatID int64, messageThreadID int, ownerTelegramID int64) (*ExecutionContext, error) {
	if chatID == 0 {
		return nil, errors.New("tool execution chat ID is required")
	}
	if ownerTelegramID <= 0 {
		return nil, errors.New("tool execution owner Telegram ID must be positive")
	}
	return &ExecutionContext{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		OwnerTelegramID: ownerTelegramID,
	}, nil
}

// WithExecutionContext attaches trusted execution data to a standard Go
// context so the Tool interface remains provider-independent.
func WithExecutionContext(applicationContext context.Context, toolExecutionContext *ExecutionContext) context.Context {
	return context.WithValue(applicationContext, executionContextKey{}, toolExecutionContext)
}

// ExecutionContextFromContext retrieves trusted execution data for a native
// tool and reports whether the caller supplied a valid context.
func ExecutionContextFromContext(applicationContext context.Context) (*ExecutionContext, bool) {
	toolExecutionContext, found := applicationContext.Value(executionContextKey{}).(*ExecutionContext)
	return toolExecutionContext, found && toolExecutionContext != nil
}

// RecordCreatedDraft records the draft created during this generation so the
// Telegram handler can present its canonical preview after the model finishes.
func (toolExecutionContext *ExecutionContext) RecordCreatedDraft(createdDraft *models.Draft) {
	toolExecutionContext.createdDraft = createdDraft
}

// CreatedDraft returns the most recent draft created during this generation.
// The result is only set by the native create_draft tool after persistence.
func (toolExecutionContext *ExecutionContext) CreatedDraft() *models.Draft {
	return toolExecutionContext.createdDraft
}

// RecordUpdatedDraft records the draft atomically changed by update_draft so
// the Telegram handler can present its newest canonical version.
func (toolExecutionContext *ExecutionContext) RecordUpdatedDraft(updatedDraft *models.Draft) {
	toolExecutionContext.updatedDraft = updatedDraft
}

// UpdatedDraft returns the draft changed during this generation, if any.
func (toolExecutionContext *ExecutionContext) UpdatedDraft() *models.Draft {
	return toolExecutionContext.updatedDraft
}

// RecordDownloadedFile queues one temporary file for delivery after the model
// has completed its response. Only native tools with the trusted context can
// call this; model-provided arguments never select a filesystem path.
func (toolExecutionContext *ExecutionContext) RecordDownloadedFile(downloadedFile DownloadedFile) {
	toolExecutionContext.downloadedFiles = append(toolExecutionContext.downloadedFiles, downloadedFile)
}

// DownloadedFiles returns a copy of the files queued during this generation.
func (toolExecutionContext *ExecutionContext) DownloadedFiles() []DownloadedFile {
	return append([]DownloadedFile(nil), toolExecutionContext.downloadedFiles...)
}

// RenameDownloadedFile updates one file queued during this generation after a
// trusted native tool has safely moved it inside its managed workspace.
func (toolExecutionContext *ExecutionContext) RenameDownloadedFile(currentFilename string, newFilename string, newPath string) error {
	matchingIndex := -1
	for fileIndex, downloadedFile := range toolExecutionContext.downloadedFiles {
		if downloadedFile.Filename != currentFilename {
			continue
		}
		if matchingIndex >= 0 {
			return errors.New("more than one downloaded file has that filename")
		}
		matchingIndex = fileIndex
	}
	if matchingIndex < 0 {
		return errors.New("downloaded file was not found in this conversation")
	}
	for fileIndex, downloadedFile := range toolExecutionContext.downloadedFiles {
		if fileIndex != matchingIndex && downloadedFile.Filename == newFilename {
			return errors.New("another downloaded file already has the requested filename")
		}
	}
	toolExecutionContext.downloadedFiles[matchingIndex].Filename = newFilename
	toolExecutionContext.downloadedFiles[matchingIndex].Path = newPath
	return nil
}
