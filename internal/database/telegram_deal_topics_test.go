package database

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestTelegramDealTopicRepositoryStoresOnlyAssociation exercises the mapping
// repository against PostgreSQL without touching the migrated application table.
func TestTelegramDealTopicRepositoryStoresOnlyAssociation(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for the PostgreSQL deal-topic repository test")
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
		CREATE TEMP TABLE telegram_deal_topics (
			deal_id TEXT PRIMARY KEY,
			message_thread_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`).Error; err != nil {
		t.Fatalf("create temporary telegram_deal_topics table: %v", err)
	}

	if _, err := FindTelegramDealTopic(context.Background(), databaseConnection, "2034020000000489080"); !errors.Is(err, ErrTelegramDealTopicNotFound) {
		t.Fatalf("FindTelegramDealTopic missing error = %v, want ErrTelegramDealTopicNotFound", err)
	}
	createdTopic, err := CreateTelegramDealTopic(context.Background(), databaseConnection, "2034020000000489080", 456)
	if err != nil {
		t.Fatalf("CreateTelegramDealTopic returned error: %v", err)
	}
	if createdTopic.DealID != "2034020000000489080" || createdTopic.MessageThreadID != 456 || createdTopic.CreatedAt.IsZero() {
		t.Errorf("created topic = %#v", createdTopic)
	}
	retrievedTopic, err := FindTelegramDealTopic(context.Background(), databaseConnection, "2034020000000489080")
	if err != nil {
		t.Fatalf("FindTelegramDealTopic returned error: %v", err)
	}
	if retrievedTopic.DealID != createdTopic.DealID || retrievedTopic.MessageThreadID != createdTopic.MessageThreadID {
		t.Errorf("retrieved topic = %#v, want %#v", retrievedTopic, createdTopic)
	}
	topicResolvedFromThread, err := FindTelegramDealTopicByMessageThreadID(context.Background(), databaseConnection, 456)
	if err != nil {
		t.Fatalf("FindTelegramDealTopicByMessageThreadID returned error: %v", err)
	}
	if topicResolvedFromThread.DealID != "2034020000000489080" {
		t.Errorf("topic resolved from thread = %#v", topicResolvedFromThread)
	}
	if err := DeleteTelegramDealTopic(context.Background(), databaseConnection, "2034020000000489080"); err != nil {
		t.Fatalf("DeleteTelegramDealTopic returned error: %v", err)
	}
	if _, err := FindTelegramDealTopic(context.Background(), databaseConnection, "2034020000000489080"); !errors.Is(err, ErrTelegramDealTopicNotFound) {
		t.Errorf("FindTelegramDealTopic after deletion error = %v, want ErrTelegramDealTopicNotFound", err)
	}
}
