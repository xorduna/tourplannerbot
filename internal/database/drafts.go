package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tourplannerbot/internal/models"

	"gorm.io/gorm"
)

// ErrDraftNotFound indicates that no draft exists for the requested identifier.
var ErrDraftNotFound = errors.New("draft not found")

// DraftRevisionConflictError contains the current draft after an update used a
// stale revision. Callers can return it to the editor without losing changes.
type DraftRevisionConflictError struct {
	CurrentDraft *models.Draft
}

// Error returns the stable revision-conflict message.
func (draftRevisionConflictError *DraftRevisionConflictError) Error() string {
	return "draft revision conflict"
}

// CreateDraftInput contains the trusted conversation and owner values used to
// create a draft. Callers never supply an ID, status, revision, or body JSON.
type CreateDraftInput struct {
	ChatID          int64
	MessageThreadID int
	OwnerTelegramID int64
	Kind            models.DraftKind
	Subject         *string
	ContentJSON     json.RawMessage
	BodyText        string
}

// UpdateDraftInput contains the canonical validated values for an optimistic
// draft update. The editor's body text is always projected server-side.
type UpdateDraftInput struct {
	ID               string
	OwnerTelegramID  int64
	ExpectedRevision int
	Subject          *string
	ContentJSON      json.RawMessage
	BodyText         string
}

// CreateDraft supersedes the active draft for a conversation and creates a new
// revision-one draft in the same transaction. A PostgreSQL advisory lock makes
// concurrent creations for that conversation deterministic.
func CreateDraft(applicationContext context.Context, databaseConnection *gorm.DB, createDraftInput CreateDraftInput) (*models.Draft, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	if createDraftInput.ChatID == 0 {
		return nil, errors.New("draft chat ID is required")
	}
	if createDraftInput.OwnerTelegramID <= 0 {
		return nil, errors.New("draft owner Telegram ID must be positive")
	}
	if !createDraftInput.Kind.IsValid() {
		return nil, fmt.Errorf("unsupported draft kind %q", createDraftInput.Kind)
	}

	canonicalBodyText := strings.ReplaceAll(strings.ReplaceAll(createDraftInput.BodyText, "\r\n", "\n"), "\r", "\n")
	contentJSON := createDraftInput.ContentJSON
	if len(contentJSON) == 0 {
		var err error
		contentJSON, err = models.NewTiptapDocumentFromPlainText(canonicalBodyText)
		if err != nil {
			return nil, fmt.Errorf("convert draft text to Tiptap document: %w", err)
		}
	} else {
		var err error
		canonicalBodyText, err = models.ValidateAndProjectTiptapDocument(contentJSON)
		if err != nil {
			return nil, fmt.Errorf("validate draft content: %w", err)
		}
	}
	draftID, err := newDraftUUID()
	if err != nil {
		return nil, fmt.Errorf("generate draft ID: %w", err)
	}
	createdDraft := &models.Draft{
		ID:              draftID,
		ChatID:          createDraftInput.ChatID,
		MessageThreadID: createDraftInput.MessageThreadID,
		OwnerTelegramID: createDraftInput.OwnerTelegramID,
		Kind:            createDraftInput.Kind,
		Subject:         createDraftInput.Subject,
		ContentJSON:     contentJSON,
		BodyText:        canonicalBodyText,
		Status:          models.DraftStatusActive,
		Revision:        1,
	}

	if err := databaseConnection.WithContext(applicationContext).Transaction(func(transaction *gorm.DB) error {
		if err := lockDraftConversation(transaction, createDraftInput.ChatID, createDraftInput.MessageThreadID); err != nil {
			return err
		}
		if err := transaction.Model(&models.Draft{}).
			Where("chat_id = ? AND message_thread_id = ? AND status = ?", createDraftInput.ChatID, createDraftInput.MessageThreadID, models.DraftStatusActive).
			Update("status", models.DraftStatusSuperseded).
			Error; err != nil {
			return fmt.Errorf("supersede active draft: %w", err)
		}
		if err := transaction.Create(createdDraft).Error; err != nil {
			return fmt.Errorf("insert draft: %w", err)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("create draft transaction: %w", err)
	}
	return createdDraft, nil
}

// FindDraftByID retrieves one draft without applying user authorization. HTTP
// handlers must validate their authenticated user before returning its content.
func FindDraftByID(applicationContext context.Context, databaseConnection *gorm.DB, draftID string) (*models.Draft, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	var draft models.Draft
	if err := databaseConnection.WithContext(applicationContext).First(&draft, "id = ?", draftID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDraftNotFound
		}
		return nil, fmt.Errorf("query draft: %w", err)
	}
	return &draft, nil
}

// FindActiveDraft retrieves the active draft owned by one Telegram user in a
// conversation. It deliberately returns ErrDraftNotFound for another user's
// draft so callers do not leak its existence or content.
func FindActiveDraft(applicationContext context.Context, databaseConnection *gorm.DB, chatID int64, messageThreadID int, ownerTelegramID int64) (*models.Draft, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	var draft models.Draft
	if err := databaseConnection.WithContext(applicationContext).
		Where("chat_id = ? AND message_thread_id = ? AND owner_telegram_id = ? AND status = ?", chatID, messageThreadID, ownerTelegramID, models.DraftStatusActive).
		First(&draft).
		Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDraftNotFound
		}
		return nil, fmt.Errorf("query active draft: %w", err)
	}
	return &draft, nil
}

// UpdateDraft atomically replaces a draft only if its owner and expected
// revision still match. A stale editor receives DraftRevisionConflictError with
// the current authorized version instead of overwriting it.
func UpdateDraft(applicationContext context.Context, databaseConnection *gorm.DB, updateDraftInput UpdateDraftInput) (*models.Draft, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	if updateDraftInput.ID == "" || updateDraftInput.OwnerTelegramID <= 0 || updateDraftInput.ExpectedRevision < 1 {
		return nil, errors.New("valid draft ID, owner Telegram ID, and expected revision are required")
	}

	var updatedDraft models.Draft
	err := databaseConnection.WithContext(applicationContext).Transaction(func(transaction *gorm.DB) error {
		updateResult := transaction.Model(&models.Draft{}).
			Where("id = ? AND owner_telegram_id = ? AND revision = ?", updateDraftInput.ID, updateDraftInput.OwnerTelegramID, updateDraftInput.ExpectedRevision).
			Updates(map[string]any{
				"subject":      updateDraftInput.Subject,
				"content_json": gorm.Expr("?::jsonb", string(updateDraftInput.ContentJSON)),
				"body_text":    updateDraftInput.BodyText,
				"revision":     gorm.Expr("revision + 1"),
				"updated_at":   gorm.Expr("CURRENT_TIMESTAMP"),
			})
		if updateResult.Error != nil {
			return fmt.Errorf("update draft: %w", updateResult.Error)
		}
		if updateResult.RowsAffected == 1 {
			if err := transaction.First(&updatedDraft, "id = ?", updateDraftInput.ID).Error; err != nil {
				return fmt.Errorf("read updated draft: %w", err)
			}
			return nil
		}

		currentDraft, err := FindDraftByID(applicationContext, transaction, updateDraftInput.ID)
		if errors.Is(err, ErrDraftNotFound) || (err == nil && currentDraft.OwnerTelegramID != updateDraftInput.OwnerTelegramID) {
			return ErrDraftNotFound
		}
		if err != nil {
			return err
		}
		return &DraftRevisionConflictError{CurrentDraft: currentDraft}
	})
	if err != nil {
		return nil, err
	}
	return &updatedDraft, nil
}

// SetDraftTelegramMessageID records the Telegram preview message associated
// with an authorized draft. Content and revision are deliberately untouched so
// presentation bookkeeping cannot overwrite an editor change.
func SetDraftTelegramMessageID(applicationContext context.Context, databaseConnection *gorm.DB, draftID string, ownerTelegramID int64, telegramMessageID int64) error {
	if databaseConnection == nil {
		return errors.New("database connection is required")
	}
	if draftID == "" || ownerTelegramID <= 0 || telegramMessageID <= 0 {
		return errors.New("valid draft ID, owner Telegram ID, and Telegram message ID are required")
	}
	updateResult := databaseConnection.WithContext(applicationContext).
		Model(&models.Draft{}).
		Where("id = ? AND owner_telegram_id = ?", draftID, ownerTelegramID).
		Update("telegram_message_id", telegramMessageID)
	if updateResult.Error != nil {
		return fmt.Errorf("set draft Telegram message ID: %w", updateResult.Error)
	}
	if updateResult.RowsAffected == 0 {
		return ErrDraftNotFound
	}
	return nil
}

// lockDraftConversation serializes draft creation for one chat and thread for
// the duration of a PostgreSQL transaction.
func lockDraftConversation(transaction *gorm.DB, chatID int64, messageThreadID int) error {
	conversationKey := fmt.Sprintf("%d:%d", chatID, messageThreadID)
	if err := transaction.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?::text, 0))", conversationKey).Error; err != nil {
		return fmt.Errorf("lock draft conversation: %w", err)
	}
	return nil
}

// newDraftUUID returns a random RFC 4122 version-4 UUID without adding a
// dependency solely for identifier generation.
func newDraftUUID() (string, error) {
	identifierBytes := make([]byte, 16)
	if _, err := rand.Read(identifierBytes); err != nil {
		return "", err
	}
	identifierBytes[6] = (identifierBytes[6] & 0x0f) | 0x40
	identifierBytes[8] = (identifierBytes[8] & 0x3f) | 0x80
	encodedIdentifier := hex.EncodeToString(identifierBytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encodedIdentifier[0:8], encodedIdentifier[8:12], encodedIdentifier[12:16], encodedIdentifier[16:20], encodedIdentifier[20:32]), nil
}
