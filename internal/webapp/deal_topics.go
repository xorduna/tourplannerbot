package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"tourplannerbot/internal/database"
	databaseModels "tourplannerbot/internal/models"

	telegramBot "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"
	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

var (
	// ErrDealTopicServiceUnavailable indicates that a startup dependency has
	// not become available yet.
	ErrDealTopicServiceUnavailable = errors.New("deal topic service is unavailable")
	dealIDPattern                  = regexp.MustCompile(`^[0-9]+$`)
)

// DealTopicStore persists only the association between a Bigin deal and a
// Telegram topic.
type DealTopicStore interface {
	FindTelegramDealTopic(applicationContext context.Context, dealID string) (*databaseModels.TelegramDealTopic, error)
	CreateTelegramDealTopic(applicationContext context.Context, dealID string, messageThreadID int64) (*databaseModels.TelegramDealTopic, error)
	DeleteTelegramDealTopic(applicationContext context.Context, dealID string) error
}

// BiginDealReader retrieves the complete current deal from Bigin.
type BiginDealReader interface {
	GetDeal(applicationContext context.Context, dealID string) (json.RawMessage, error)
}

// DealTopicIntroductionGenerator summarizes current Bigin data for the first
// message sent into a newly created topic.
type DealTopicIntroductionGenerator interface {
	GenerateIntroduction(applicationContext context.Context, dealID string, biginResponse json.RawMessage) (string, error)
}

// TelegramForumTopicCreator verifies and creates topics through the Telegram Bot API.
type TelegramForumTopicCreator interface {
	CreateForumTopic(applicationContext context.Context, parameters *telegramBot.CreateForumTopicParams) (*telegramModels.ForumTopic, error)
	SendChatAction(applicationContext context.Context, parameters *telegramBot.SendChatActionParams) (bool, error)
	SendMessage(applicationContext context.Context, parameters *telegramBot.SendMessageParams) (*telegramModels.Message, error)
}

// GORMDealTopicStore provides database-backed deal-topic associations. Its
// connection is supplied after the application's startup retry succeeds.
type GORMDealTopicStore struct {
	databaseConnectionMutex sync.RWMutex
	databaseConnection      *gorm.DB
}

// NewGORMDealTopicStore creates a store without a database connection.
func NewGORMDealTopicStore() *GORMDealTopicStore {
	return &GORMDealTopicStore{}
}

// SetDatabaseConnection makes a GORM connection available to the store.
func (dealTopicStore *GORMDealTopicStore) SetDatabaseConnection(databaseConnection *gorm.DB) {
	dealTopicStore.databaseConnectionMutex.Lock()
	defer dealTopicStore.databaseConnectionMutex.Unlock()
	dealTopicStore.databaseConnection = databaseConnection
}

// FindTelegramDealTopic retrieves one persisted deal-topic association.
func (dealTopicStore *GORMDealTopicStore) FindTelegramDealTopic(applicationContext context.Context, dealID string) (*databaseModels.TelegramDealTopic, error) {
	databaseConnection := dealTopicStore.currentDatabaseConnection()
	if databaseConnection == nil {
		return nil, ErrDealTopicServiceUnavailable
	}
	return database.FindTelegramDealTopic(applicationContext, databaseConnection, dealID)
}

// CreateTelegramDealTopic persists one deal-topic association.
func (dealTopicStore *GORMDealTopicStore) CreateTelegramDealTopic(applicationContext context.Context, dealID string, messageThreadID int64) (*databaseModels.TelegramDealTopic, error) {
	databaseConnection := dealTopicStore.currentDatabaseConnection()
	if databaseConnection == nil {
		return nil, ErrDealTopicServiceUnavailable
	}
	return database.CreateTelegramDealTopic(applicationContext, databaseConnection, dealID, messageThreadID)
}

// DeleteTelegramDealTopic removes one stale deal-topic association.
func (dealTopicStore *GORMDealTopicStore) DeleteTelegramDealTopic(applicationContext context.Context, dealID string) error {
	databaseConnection := dealTopicStore.currentDatabaseConnection()
	if databaseConnection == nil {
		return ErrDealTopicServiceUnavailable
	}
	return database.DeleteTelegramDealTopic(applicationContext, databaseConnection, dealID)
}

// currentDatabaseConnection safely snapshots the current connection.
func (dealTopicStore *GORMDealTopicStore) currentDatabaseConnection() *gorm.DB {
	dealTopicStore.databaseConnectionMutex.RLock()
	defer dealTopicStore.databaseConnectionMutex.RUnlock()
	return dealTopicStore.databaseConnection
}

// DealTopicService resolves existing associations and creates missing Telegram
// topics from current Bigin deal names.
type DealTopicService struct {
	telegramGroupChatID    int64
	telegramInternalChatID string
	dealTopicStore         DealTopicStore
	logger                 *slog.Logger

	dependencyMutex           sync.RWMutex
	biginDealReader           BiginDealReader
	introductionGenerator     DealTopicIntroductionGenerator
	telegramForumTopicCreator TelegramForumTopicCreator
	resolutionMutex           sync.Mutex
}

// NewDealTopicService creates a resolver for one private Telegram supergroup.
func NewDealTopicService(telegramGroupChatID int64, dealTopicStore DealTopicStore) (*DealTopicService, error) {
	if dealTopicStore == nil {
		return nil, errors.New("deal topic store is required")
	}
	telegramGroupChatIDText := strconv.FormatInt(telegramGroupChatID, 10)
	if !strings.HasPrefix(telegramGroupChatIDText, "-100") || len(telegramGroupChatIDText) == len("-100") {
		return nil, errors.New("Telegram group chat ID must start with -100")
	}
	telegramInternalChatID := strings.TrimPrefix(telegramGroupChatIDText, "-100")
	if !dealIDPattern.MatchString(telegramInternalChatID) {
		return nil, errors.New("Telegram group chat ID must contain only digits after -100")
	}

	return &DealTopicService{
		telegramGroupChatID:    telegramGroupChatID,
		telegramInternalChatID: telegramInternalChatID,
		dealTopicStore:         dealTopicStore,
		logger:                 slog.Default(),
	}, nil
}

// SetBiginDealReader makes the Bigin integration available to requests
// that need to create a topic.
func (dealTopicService *DealTopicService) SetBiginDealReader(biginDealReader BiginDealReader) {
	dealTopicService.dependencyMutex.Lock()
	defer dealTopicService.dependencyMutex.Unlock()
	dealTopicService.biginDealReader = biginDealReader
}

// SetIntroductionGenerator enables an LLM-generated first message for newly
// created topics. A deterministic message is used when no generator is set.
func (dealTopicService *DealTopicService) SetIntroductionGenerator(introductionGenerator DealTopicIntroductionGenerator) {
	dealTopicService.dependencyMutex.Lock()
	defer dealTopicService.dependencyMutex.Unlock()
	dealTopicService.introductionGenerator = introductionGenerator
}

// SetLogger configures warnings for non-fatal introduction failures.
func (dealTopicService *DealTopicService) SetLogger(logger *slog.Logger) {
	if logger != nil {
		dealTopicService.logger = logger
	}
}

// SetTelegramForumTopicCreator makes the initialized Telegram bot available to
// requests that need to create a topic.
func (dealTopicService *DealTopicService) SetTelegramForumTopicCreator(telegramForumTopicCreator TelegramForumTopicCreator) {
	dealTopicService.dependencyMutex.Lock()
	defer dealTopicService.dependencyMutex.Unlock()
	dealTopicService.telegramForumTopicCreator = telegramForumTopicCreator
}

// ResolveTopicURL verifies a persisted topic with Telegram, recreating and
// recording it when Telegram confirms that it was deleted. The process-wide
// mutex prevents duplicate recreation by concurrent requests.
func (dealTopicService *DealTopicService) ResolveTopicURL(applicationContext context.Context, dealID string) (string, error) {
	dealID = strings.TrimSpace(dealID)
	if !dealIDPattern.MatchString(dealID) {
		return "", errors.New("deal ID must contain only digits")
	}

	dealTopicService.resolutionMutex.Lock()
	defer dealTopicService.resolutionMutex.Unlock()

	telegramDealTopic, err := dealTopicService.dealTopicStore.FindTelegramDealTopic(applicationContext, dealID)
	if err == nil {
		telegramForumTopicCreator := dealTopicService.currentTelegramForumTopicCreator()
		if telegramForumTopicCreator == nil {
			return "", ErrDealTopicServiceUnavailable
		}
		topicExists, verificationError := telegramTopicExists(applicationContext, telegramForumTopicCreator, dealTopicService.telegramGroupChatID, telegramDealTopic.MessageThreadID)
		if verificationError != nil {
			return "", fmt.Errorf("verify Telegram forum topic: %w", verificationError)
		}
		if topicExists {
			return dealTopicService.topicURL(telegramDealTopic.MessageThreadID), nil
		}
		if err := dealTopicService.dealTopicStore.DeleteTelegramDealTopic(applicationContext, dealID); err != nil {
			return "", err
		}
	}
	if err != nil && !errors.Is(err, database.ErrTelegramDealTopicNotFound) {
		return "", err
	}

	biginDealReader, introductionGenerator, telegramForumTopicCreator := dealTopicService.currentCreationDependencies()
	if biginDealReader == nil || telegramForumTopicCreator == nil {
		return "", ErrDealTopicServiceUnavailable
	}
	biginResponse, err := biginDealReader.GetDeal(applicationContext, dealID)
	if err != nil {
		return "", fmt.Errorf("retrieve Bigin deal: %w", err)
	}
	dealName, err := dealNameFromBiginResponse(dealID, biginResponse)
	if err != nil {
		return "", err
	}
	forumTopic, err := telegramForumTopicCreator.CreateForumTopic(applicationContext, &telegramBot.CreateForumTopicParams{
		ChatID: dealTopicService.telegramGroupChatID,
		Name:   dealName,
	})
	if err != nil {
		return "", fmt.Errorf("create Telegram forum topic: %w", err)
	}
	if forumTopic == nil || forumTopic.MessageThreadID <= 0 {
		return "", errors.New("Telegram returned an invalid forum topic")
	}

	telegramDealTopic, err = dealTopicService.dealTopicStore.CreateTelegramDealTopic(applicationContext, dealID, int64(forumTopic.MessageThreadID))
	if err != nil {
		return "", err
	}
	dealTopicService.sendTopicIntroduction(applicationContext, introductionGenerator, telegramForumTopicCreator, dealID, dealName, biginResponse, telegramDealTopic.MessageThreadID)
	return dealTopicService.topicURL(telegramDealTopic.MessageThreadID), nil
}

// dealNameFromBiginResponse extracts the topic name from a current Bigin deal envelope.
func dealNameFromBiginResponse(dealID string, biginResponse json.RawMessage) (string, error) {
	responseEnvelope := struct {
		Data []struct {
			Name string `json:"Deal_Name"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(biginResponse, &responseEnvelope); err != nil {
		return "", fmt.Errorf("decode Bigin deal %s: %w", dealID, err)
	}
	if len(responseEnvelope.Data) == 0 {
		return "", fmt.Errorf("Bigin deal %s was not found", dealID)
	}
	dealName := strings.TrimSpace(responseEnvelope.Data[0].Name)
	if dealName == "" {
		return "", fmt.Errorf("Bigin deal %s has no name", dealID)
	}
	return dealName, nil
}

// sendTopicIntroduction sends an ephemeral summary to a newly created topic.
// Generation and delivery failures are non-fatal because the topic mapping is
// already valid and the user must still be redirected to it.
func (dealTopicService *DealTopicService) sendTopicIntroduction(applicationContext context.Context, introductionGenerator DealTopicIntroductionGenerator, telegramForumTopicCreator TelegramForumTopicCreator, dealID string, dealName string, biginResponse json.RawMessage, messageThreadID int64) {
	introductionText := fallbackTopicIntroduction(dealID, dealName)
	if introductionGenerator != nil {
		generatedIntroduction, err := introductionGenerator.GenerateIntroduction(applicationContext, dealID, biginResponse)
		if err != nil {
			dealTopicService.logger.Warn("failed to generate Telegram topic introduction; using fallback", "deal_id", dealID, "error", err)
		} else if strings.TrimSpace(generatedIntroduction) != "" {
			introductionText = generatedIntroduction
		}
	}
	if _, err := telegramForumTopicCreator.SendMessage(applicationContext, &telegramBot.SendMessageParams{
		ChatID:          dealTopicService.telegramGroupChatID,
		MessageThreadID: int(messageThreadID),
		Text:            introductionText,
	}); err != nil {
		dealTopicService.logger.Warn("failed to send Telegram topic introduction", "deal_id", dealID, "message_thread_id", messageThreadID, "error", err)
	}
}

// fallbackTopicIntroduction remains useful when LLM generation is unavailable.
func fallbackTopicIntroduction(dealID string, dealName string) string {
	return fmt.Sprintf("👋 Tour vinculat a Bigin\n\n%s\nDeal ID: %s\n\nJa tinc accés a la fitxa actual de Bigin. Pots demanar-me que prepari l’itinerari, revisi la informació del client o redacti una comunicació.", dealName, dealID)
}

// telegramTopicExists performs the lightest Bot API operation scoped to a
// topic. Only Telegram's explicit missing-topic response is treated as absent;
// transient and permission errors preserve the mapping and abort the request.
func telegramTopicExists(applicationContext context.Context, telegramForumTopicCreator TelegramForumTopicCreator, telegramGroupChatID int64, messageThreadID int64) (bool, error) {
	actionSucceeded, err := telegramForumTopicCreator.SendChatAction(applicationContext, &telegramBot.SendChatActionParams{
		ChatID:          telegramGroupChatID,
		MessageThreadID: int(messageThreadID),
		Action:          telegramModels.ChatActionTyping,
	})
	if err == nil {
		if !actionSucceeded {
			return false, errors.New("Telegram did not confirm the forum topic")
		}
		return true, nil
	}
	normalizedError := strings.ToLower(err.Error())
	if errors.Is(err, telegramBot.ErrorBadRequest) || errors.Is(err, telegramBot.ErrorNotFound) {
		if strings.Contains(normalizedError, "message thread not found") || strings.Contains(normalizedError, "topic not found") {
			return false, nil
		}
		if strings.Contains(normalizedError, "topic_closed") || strings.Contains(normalizedError, "topic is closed") {
			return true, nil
		}
	}
	return false, err
}

// currentCreationDependencies safely snapshots external API dependencies.
func (dealTopicService *DealTopicService) currentCreationDependencies() (BiginDealReader, DealTopicIntroductionGenerator, TelegramForumTopicCreator) {
	dealTopicService.dependencyMutex.RLock()
	defer dealTopicService.dependencyMutex.RUnlock()
	return dealTopicService.biginDealReader, dealTopicService.introductionGenerator, dealTopicService.telegramForumTopicCreator
}

// currentTelegramForumTopicCreator safely snapshots the Telegram dependency.
func (dealTopicService *DealTopicService) currentTelegramForumTopicCreator() TelegramForumTopicCreator {
	dealTopicService.dependencyMutex.RLock()
	defer dealTopicService.dependencyMutex.RUnlock()
	return dealTopicService.telegramForumTopicCreator
}

// topicURL builds Telegram's private-supergroup topic URL.
func (dealTopicService *DealTopicService) topicURL(messageThreadID int64) string {
	return fmt.Sprintf("https://t.me/c/%s/%d", dealTopicService.telegramInternalChatID, messageThreadID)
}

// RegisterDealTopicRoutes registers the public Bigin deal-to-Telegram topic endpoint.
func RegisterDealTopicRoutes(echoServer *echo.Echo, dealTopicService *DealTopicService) {
	if echoServer == nil || dealTopicService == nil {
		return
	}
	echoServer.GET("/deals/:deal_id/topic", func(echoContext *echo.Context) error {
		dealID := echoContext.Param("deal_id")
		if !dealIDPattern.MatchString(strings.TrimSpace(dealID)) {
			return echoContext.String(http.StatusBadRequest, "invalid deal ID\n")
		}
		topicURL, err := dealTopicService.ResolveTopicURL(echoContext.Request().Context(), dealID)
		if errors.Is(err, ErrDealTopicServiceUnavailable) {
			return echoContext.String(http.StatusServiceUnavailable, "deal topic service unavailable\n")
		}
		if err != nil {
			return fmt.Errorf("resolve Telegram topic for Bigin deal %s: %w", dealID, err)
		}
		return echoContext.Redirect(http.StatusFound, topicURL)
	})
}
