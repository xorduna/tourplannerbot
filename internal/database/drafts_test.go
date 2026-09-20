package database

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"tourplannerbot/internal/models"
)

// TestDraftRepositoryCreatesSupersedesAndSerializes uses a temporary PostgreSQL
// table so it exercises the production transaction and partial unique index
// without modifying the application's migrated drafts table.
func TestDraftRepositoryCreatesSupersedesAndSerializes(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for the PostgreSQL draft repository test")
	}
	databaseConnection, err := Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL test connection: %v", err)
	}
	sqlDatabaseConnection, err := databaseConnection.DB()
	if err != nil {
		t.Fatalf("access PostgreSQL test connection: %v", err)
	}
	t.Cleanup(func() { _ = sqlDatabaseConnection.Close() })
	sqlDatabaseConnection.SetMaxOpenConns(1)

	if err := databaseConnection.Exec(`
		CREATE TEMP TABLE drafts (
			id UUID PRIMARY KEY,
			chat_id BIGINT NOT NULL,
			message_thread_id INTEGER NOT NULL DEFAULT 0,
			owner_telegram_id BIGINT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('email', 'whatsapp', 'generic')),
			subject TEXT,
			content_json JSONB NOT NULL,
			body_text TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('active', 'superseded')),
			revision INTEGER NOT NULL DEFAULT 1,
			telegram_message_id BIGINT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`).Error; err != nil {
		t.Fatalf("create temporary drafts table: %v", err)
	}
	if err := databaseConnection.Exec(`
		CREATE UNIQUE INDEX drafts_one_active_per_conversation
		ON drafts (chat_id, message_thread_id)
		WHERE status = 'active'`).Error; err != nil {
		t.Fatalf("create temporary active-draft index: %v", err)
	}

	firstDraft, err := CreateDraft(context.Background(), databaseConnection, CreateDraftInput{
		ChatID: 100, OwnerTelegramID: 42, Kind: models.DraftKindWhatsApp, BodyText: "Primer text",
	})
	if err != nil {
		t.Fatalf("CreateDraft first draft returned error: %v", err)
	}
	if firstDraft.Revision != 1 || firstDraft.Status != models.DraftStatusActive {
		t.Errorf("first draft = %#v, want active revision one", firstDraft)
	}
	secondDraft, err := CreateDraft(context.Background(), databaseConnection, CreateDraftInput{
		ChatID: 100, OwnerTelegramID: 42, Kind: models.DraftKindEmail, BodyText: "Segon text",
	})
	if err != nil {
		t.Fatalf("CreateDraft replacement draft returned error: %v", err)
	}
	retrievedFirstDraft, err := FindDraftByID(context.Background(), databaseConnection, firstDraft.ID)
	if err != nil {
		t.Fatalf("FindDraftByID first draft returned error: %v", err)
	}
	if retrievedFirstDraft.Status != models.DraftStatusSuperseded {
		t.Errorf("first draft status = %q, want %q", retrievedFirstDraft.Status, models.DraftStatusSuperseded)
	}
	if secondDraft.Status != models.DraftStatusActive || secondDraft.Revision != 1 {
		t.Errorf("second draft = %#v, want active revision one", secondDraft)
	}
	activeDraft, err := FindActiveDraft(context.Background(), databaseConnection, 100, 0, 42)
	if err != nil || activeDraft.ID != secondDraft.ID {
		t.Errorf("FindActiveDraft = (%#v, %v), want second draft", activeDraft, err)
	}
	if _, err := FindActiveDraft(context.Background(), databaseConnection, 100, 0, 99); !errors.Is(err, ErrDraftNotFound) {
		t.Errorf("FindActiveDraft for another owner error = %v, want ErrDraftNotFound", err)
	}
	updatedContentJSON, err := models.NewTiptapDocumentFromPlainText("Text actualitzat")
	if err != nil {
		t.Fatalf("create updated content: %v", err)
	}
	updatedDraft, err := UpdateDraft(context.Background(), databaseConnection, UpdateDraftInput{
		ID:               secondDraft.ID,
		OwnerTelegramID:  42,
		ExpectedRevision: 1,
		ContentJSON:      updatedContentJSON,
		BodyText:         "Text actualitzat",
	})
	if err != nil {
		t.Fatalf("UpdateDraft returned error: %v", err)
	}
	if updatedDraft.Revision != 2 || updatedDraft.BodyText != "Text actualitzat" {
		t.Errorf("updated draft = %#v, want revision two and updated body", updatedDraft)
	}
	_, staleUpdateError := UpdateDraft(context.Background(), databaseConnection, UpdateDraftInput{
		ID:               secondDraft.ID,
		OwnerTelegramID:  42,
		ExpectedRevision: 1,
		ContentJSON:      updatedContentJSON,
		BodyText:         "Stale overwrite",
	})
	var revisionConflictError *DraftRevisionConflictError
	if !errors.As(staleUpdateError, &revisionConflictError) || revisionConflictError.CurrentDraft.Revision != 2 || revisionConflictError.CurrentDraft.BodyText != "Text actualitzat" {
		t.Errorf("stale UpdateDraft error = %#v, want revision conflict with current draft", staleUpdateError)
	}
	duplicateDraftID, err := newDraftUUID()
	if err != nil {
		t.Fatalf("generate duplicate draft ID: %v", err)
	}
	duplicateContentJSON, err := models.NewTiptapDocumentFromPlainText("Duplicate active draft")
	if err != nil {
		t.Fatalf("create duplicate draft content: %v", err)
	}
	if err := databaseConnection.Create(&models.Draft{
		ID:              duplicateDraftID,
		ChatID:          100,
		OwnerTelegramID: 42,
		Kind:            models.DraftKindGeneric,
		ContentJSON:     duplicateContentJSON,
		BodyText:        "Duplicate active draft",
		Status:          models.DraftStatusActive,
		Revision:        1,
	}).Error; err == nil {
		t.Error("partial unique index allowed a second active draft in the same conversation")
	}

	var creationWaitGroup sync.WaitGroup
	creationErrors := make(chan error, 2)
	for creationIndex := 0; creationIndex < 2; creationIndex++ {
		creationWaitGroup.Add(1)
		go func() {
			defer creationWaitGroup.Done()
			_, creationError := CreateDraft(context.Background(), databaseConnection, CreateDraftInput{
				ChatID: 200, OwnerTelegramID: 42, Kind: models.DraftKindGeneric, BodyText: "Concurrent text",
			})
			creationErrors <- creationError
		}()
	}
	creationWaitGroup.Wait()
	close(creationErrors)
	for creationError := range creationErrors {
		if creationError != nil {
			t.Errorf("concurrent CreateDraft returned error: %v", creationError)
		}
	}
	var activeDraftCount int64
	if err := databaseConnection.Model(&models.Draft{}).
		Where("chat_id = ? AND message_thread_id = ? AND status = ?", 200, 0, models.DraftStatusActive).
		Count(&activeDraftCount).
		Error; err != nil {
		t.Fatalf("count active concurrent drafts: %v", err)
	}
	if activeDraftCount != 1 {
		t.Errorf("active drafts after concurrent creation = %d, want 1", activeDraftCount)
	}
	if _, err := FindDraftByID(context.Background(), databaseConnection, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrDraftNotFound) {
		t.Errorf("FindDraftByID missing draft error = %v, want ErrDraftNotFound", err)
	}
}
