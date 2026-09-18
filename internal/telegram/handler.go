package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"tourplannerbot/internal/llm"
	applicationModels "tourplannerbot/internal/models"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"gorm.io/gorm"
)

const jokeInstructions = "Respond with one concise, clean, original joke related to the user's message. Reply always in catalan"

// responseGenerator produces one text response from conversation context.
type responseGenerator interface {
	Generate(ctx context.Context, instructions string, conversationMessages []llm.Message) (string, error)
}

// authorizationResult describes how the handler should respond to an access check.
type authorizationResult int

const (
	authorizationResultAllowed authorizationResult = iota
	authorizationResultPINRequired
	authorizationResultPINIncorrect
	authorizationResultAccessGranted
)

// Handler handles incoming Telegram messages and routes them appropriately.
type Handler struct {
	logger                              *slog.Logger
	databaseConnection                  *gorm.DB
	responseGenerator                   responseGenerator
	accessPIN                           string
	messageHistoryMaxMessages           int
	pendingAuthorizationByTelegramID    map[int64]struct{}
	pendingAuthorizationByTelegramMutex sync.Mutex
}

// NewHandler creates a message handler with the provided dependencies and history limit.
func NewHandler(logger *slog.Logger, databaseConnection *gorm.DB, accessPIN string, messageHistoryMaxMessages int, responseGenerator responseGenerator) *Handler {
	return &Handler{
		logger:                           logger,
		databaseConnection:               databaseConnection,
		responseGenerator:                responseGenerator,
		accessPIN:                        accessPIN,
		messageHistoryMaxMessages:        messageHistoryMaxMessages,
		pendingAuthorizationByTelegramID: make(map[int64]struct{}),
	}
}

// HandleMessage is the default handler for all incoming Telegram updates.
// It verifies access before generating an LLM response for authorized users.
func (telegramHandler *Handler) HandleMessage(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From == nil {
		return
	}

	chatID := update.Message.Chat.ID
	messageThreadID := update.Message.MessageThreadID
	incomingText := update.Message.Text
	senderUser := update.Message.From

	telegramHandler.logger.Info("received message",
		"chat_id", chatID,
		"message_thread_id", messageThreadID,
		"telegram_user_id", senderUser.ID,
		"username", senderUser.Username,
		"text_length", len(incomingText),
	)

	authorizationResult, err := telegramHandler.authorizeUser(ctx, senderUser, incomingText)
	if err != nil {
		telegramHandler.logger.Error("failed to authorize Telegram user",
			"telegram_user_id", senderUser.ID,
			"error", err,
		)
		telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "No s'ha pogut validar l'accés. Torna-ho a provar.")
		return
	}

	switch authorizationResult {
	case authorizationResultPINRequired:
		telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "Introdueix el PIN d'accés")
		return
	case authorizationResultPINIncorrect:
		telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "PIN incorrecte")
		return
	case authorizationResultAccessGranted:
		telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "✓ Accés concedit")
		return
	case authorizationResultAllowed:
		if incomingText == "" {
			return
		}
		if err := telegramHandler.saveUserMessage(ctx, chatID, messageThreadID, senderUser.ID, incomingText); err != nil {
			telegramHandler.logger.Error("failed to save user message",
				"chat_id", chatID,
				"message_thread_id", messageThreadID,
				"telegram_user_id", senderUser.ID,
				"error", err,
			)
			telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "No he pogut desar el missatge. Torna-ho a provar.")
			return
		}

		conversationMessages, err := telegramHandler.loadConversationMessages(ctx, chatID, messageThreadID)
		if err != nil {
			telegramHandler.logger.Error("failed to load conversation messages",
				"chat_id", chatID,
				"message_thread_id", messageThreadID,
				"error", err,
			)
			telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "No he pogut recuperar la conversa. Torna-ho a provar.")
			return
		}

		generatedText, err := telegramHandler.responseGenerator.Generate(ctx, jokeInstructions, conversationMessages)
		if err != nil {
			telegramHandler.logger.Error("failed to generate LLM response",
				"chat_id", chatID,
				"telegram_user_id", senderUser.ID,
				"error", err,
			)
			telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "No he pogut generar l'acudit. Torna-ho a provar.")
			return
		}
		if err := telegramHandler.saveAssistantMessage(ctx, chatID, messageThreadID, generatedText); err != nil {
			telegramHandler.logger.Error("failed to save assistant message",
				"chat_id", chatID,
				"message_thread_id", messageThreadID,
				"error", err,
			)
			telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, "No he pogut desar la resposta. Torna-ho a provar.")
			return
		}
		telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, generatedText)
	}
}

// saveUserMessage persists an authorized user's text in its Telegram conversation.
func (telegramHandler *Handler) saveUserMessage(ctx context.Context, chatID int64, messageThreadID int, userID int64, content string) error {
	return telegramHandler.databaseConnection.WithContext(ctx).Create(&applicationModels.Message{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		UserID:          &userID,
		Role:            applicationModels.MessageRoleUser,
		Content:         content,
	}).Error
}

// saveAssistantMessage persists a generated assistant response in its Telegram conversation.
func (telegramHandler *Handler) saveAssistantMessage(ctx context.Context, chatID int64, messageThreadID int, content string) error {
	return telegramHandler.databaseConnection.WithContext(ctx).Create(&applicationModels.Message{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Role:            applicationModels.MessageRoleAssistant,
		Content:         content,
	}).Error
}

// loadConversationMessages returns the newest configured messages in chronological order.
func (telegramHandler *Handler) loadConversationMessages(ctx context.Context, chatID int64, messageThreadID int) ([]llm.Message, error) {
	if telegramHandler.messageHistoryMaxMessages < 1 {
		return nil, fmt.Errorf("message history limit must be positive")
	}

	var persistedMessages []applicationModels.Message
	err := telegramHandler.databaseConnection.WithContext(ctx).
		Where("chat_id = ? AND message_thread_id = ?", chatID, messageThreadID).
		Order("created_at DESC, id DESC").
		Limit(telegramHandler.messageHistoryMaxMessages).
		Find(&persistedMessages).
		Error
	if err != nil {
		return nil, fmt.Errorf("query persisted conversation messages: %w", err)
	}

	conversationMessages := make([]llm.Message, 0, len(persistedMessages))
	for messageIndex := len(persistedMessages) - 1; messageIndex >= 0; messageIndex-- {
		persistedMessage := persistedMessages[messageIndex]
		conversationMessages = append(conversationMessages, llm.Message{
			Role:    persistedMessage.Role,
			Content: persistedMessage.Content,
		})
	}

	return conversationMessages, nil
}

// authorizeUser checks GORM directly for existing access and handles the temporary PIN prompt state.
func (telegramHandler *Handler) authorizeUser(ctx context.Context, senderUser *models.User, suppliedPIN string) (authorizationResult, error) {
	telegramHandler.pendingAuthorizationByTelegramMutex.Lock()
	defer telegramHandler.pendingAuthorizationByTelegramMutex.Unlock()

	var authorizedUserCount int64
	err := telegramHandler.databaseConnection.WithContext(ctx).
		Model(&applicationModels.AllowedUser{}).
		Where("telegram_id = ?", senderUser.ID).
		Count(&authorizedUserCount).
		Error
	if err != nil {
		return authorizationResultPINRequired, err
	}
	if authorizedUserCount > 0 {
		return authorizationResultAllowed, nil
	}

	_, isAwaitingPIN := telegramHandler.pendingAuthorizationByTelegramID[senderUser.ID]
	if !isAwaitingPIN {
		telegramHandler.pendingAuthorizationByTelegramID[senderUser.ID] = struct{}{}
		return authorizationResultPINRequired, nil
	}

	if suppliedPIN != telegramHandler.accessPIN {
		return authorizationResultPINIncorrect, nil
	}

	err = telegramHandler.databaseConnection.WithContext(ctx).Create(&applicationModels.AllowedUser{
		TelegramID: senderUser.ID,
		Name:       displayName(senderUser),
	}).Error
	if err != nil {
		return authorizationResultPINRequired, err
	}

	delete(telegramHandler.pendingAuthorizationByTelegramID, senderUser.ID)
	return authorizationResultAccessGranted, nil
}

// displayName returns the sender's name for storage in the authorization database.
func displayName(senderUser *models.User) string {
	fullName := strings.TrimSpace(strings.Join([]string{senderUser.FirstName, senderUser.LastName}, " "))
	if fullName != "" {
		return fullName
	}
	if senderUser.Username != "" {
		return "@" + senderUser.Username
	}
	return "Unknown Telegram user"
}

// sendText sends text to a Telegram chat and logs any delivery error.
func (telegramHandler *Handler) sendText(ctx context.Context, telegramBot *bot.Bot, chatID int64, messageThreadID int, text string) {
	_, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Text:            text,
	})
	if err != nil {
		telegramHandler.logger.Error("failed to send Telegram message",
			"chat_id", chatID,
			"message_thread_id", messageThreadID,
			"error", err,
		)
	}
}
