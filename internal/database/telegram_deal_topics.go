package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"tourplannerbot/internal/models"

	"gorm.io/gorm"
)

// ErrTelegramDealTopicNotFound indicates that no Telegram topic has been
// associated with the requested Bigin deal yet.
var ErrTelegramDealTopicNotFound = errors.New("Telegram deal topic not found")

// TelegramDealTopicStore exposes deal-topic persistence while allowing the
// application startup loop to provide PostgreSQL after the HTTP server starts.
type TelegramDealTopicStore struct {
	databaseConnectionMutex sync.RWMutex
	databaseConnection      *gorm.DB
}

// NewTelegramDealTopicStore creates a store whose connection can be supplied
// when PostgreSQL becomes available.
func NewTelegramDealTopicStore() *TelegramDealTopicStore {
	return &TelegramDealTopicStore{}
}

// SetDatabaseConnection makes a GORM connection available to the store.
func (dealTopicStore *TelegramDealTopicStore) SetDatabaseConnection(databaseConnection *gorm.DB) {
	dealTopicStore.databaseConnectionMutex.Lock()
	defer dealTopicStore.databaseConnectionMutex.Unlock()
	dealTopicStore.databaseConnection = databaseConnection
}

// FindTelegramDealTopic retrieves one association through the current connection.
func (dealTopicStore *TelegramDealTopicStore) FindTelegramDealTopic(applicationContext context.Context, dealID string) (*models.TelegramDealTopic, error) {
	databaseConnection := dealTopicStore.currentDatabaseConnection()
	if databaseConnection == nil {
		return nil, ErrConnectionUnavailable
	}
	return FindTelegramDealTopic(applicationContext, databaseConnection, dealID)
}

// CreateTelegramDealTopic persists one association through the current connection.
func (dealTopicStore *TelegramDealTopicStore) CreateTelegramDealTopic(applicationContext context.Context, dealID string, messageThreadID int64) (*models.TelegramDealTopic, error) {
	databaseConnection := dealTopicStore.currentDatabaseConnection()
	if databaseConnection == nil {
		return nil, ErrConnectionUnavailable
	}
	return CreateTelegramDealTopic(applicationContext, databaseConnection, dealID, messageThreadID)
}

// DeleteTelegramDealTopic removes one association through the current connection.
func (dealTopicStore *TelegramDealTopicStore) DeleteTelegramDealTopic(applicationContext context.Context, dealID string) error {
	databaseConnection := dealTopicStore.currentDatabaseConnection()
	if databaseConnection == nil {
		return ErrConnectionUnavailable
	}
	return DeleteTelegramDealTopic(applicationContext, databaseConnection, dealID)
}

// currentDatabaseConnection safely snapshots the current GORM connection.
func (dealTopicStore *TelegramDealTopicStore) currentDatabaseConnection() *gorm.DB {
	dealTopicStore.databaseConnectionMutex.RLock()
	defer dealTopicStore.databaseConnectionMutex.RUnlock()
	return dealTopicStore.databaseConnection
}

// FindTelegramDealTopic retrieves the Telegram topic association for a Bigin deal.
func FindTelegramDealTopic(applicationContext context.Context, databaseConnection *gorm.DB, dealID string) (*models.TelegramDealTopic, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	dealID = strings.TrimSpace(dealID)
	if dealID == "" {
		return nil, errors.New("deal ID is required")
	}

	var telegramDealTopic models.TelegramDealTopic
	if err := databaseConnection.WithContext(applicationContext).First(&telegramDealTopic, "deal_id = ?", dealID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTelegramDealTopicNotFound
		}
		return nil, fmt.Errorf("query Telegram deal topic: %w", err)
	}
	return &telegramDealTopic, nil
}

// FindTelegramDealTopicByMessageThreadID retrieves the Bigin deal associated
// with an incoming Telegram forum topic.
func FindTelegramDealTopicByMessageThreadID(applicationContext context.Context, databaseConnection *gorm.DB, messageThreadID int64) (*models.TelegramDealTopic, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	if messageThreadID <= 0 {
		return nil, errors.New("positive message thread ID is required")
	}

	var telegramDealTopic models.TelegramDealTopic
	if err := databaseConnection.WithContext(applicationContext).First(&telegramDealTopic, "message_thread_id = ?", messageThreadID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTelegramDealTopicNotFound
		}
		return nil, fmt.Errorf("query Telegram deal topic by message thread ID: %w", err)
	}
	return &telegramDealTopic, nil
}

// CreateTelegramDealTopic persists a newly created Telegram topic association.
func CreateTelegramDealTopic(applicationContext context.Context, databaseConnection *gorm.DB, dealID string, messageThreadID int64) (*models.TelegramDealTopic, error) {
	if databaseConnection == nil {
		return nil, errors.New("database connection is required")
	}
	dealID = strings.TrimSpace(dealID)
	if dealID == "" || messageThreadID <= 0 {
		return nil, errors.New("deal ID and positive message thread ID are required")
	}

	telegramDealTopic := &models.TelegramDealTopic{
		DealID:          dealID,
		MessageThreadID: messageThreadID,
	}
	if err := databaseConnection.WithContext(applicationContext).Create(telegramDealTopic).Error; err != nil {
		return nil, fmt.Errorf("insert Telegram deal topic: %w", err)
	}
	return telegramDealTopic, nil
}

// DeleteTelegramDealTopic removes a stale association after Telegram confirms
// that its forum topic no longer exists.
func DeleteTelegramDealTopic(applicationContext context.Context, databaseConnection *gorm.DB, dealID string) error {
	if databaseConnection == nil {
		return errors.New("database connection is required")
	}
	dealID = strings.TrimSpace(dealID)
	if dealID == "" {
		return errors.New("deal ID is required")
	}
	if err := databaseConnection.WithContext(applicationContext).
		Delete(&models.TelegramDealTopic{}, "deal_id = ?", dealID).
		Error; err != nil {
		return fmt.Errorf("delete Telegram deal topic: %w", err)
	}
	return nil
}
