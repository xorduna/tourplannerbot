package telegram

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	applicationModels "tourplannerbot/internal/models"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"gorm.io/gorm"
)

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
	accessPIN                           string
	pendingAuthorizationByTelegramID    map[int64]struct{}
	pendingAuthorizationByTelegramMutex sync.Mutex
}

// NewHandler creates a message handler with the provided logger, database connection, and access PIN.
func NewHandler(logger *slog.Logger, databaseConnection *gorm.DB, accessPIN string) *Handler {
	return &Handler{
		logger:                           logger,
		databaseConnection:               databaseConnection,
		accessPIN:                        accessPIN,
		pendingAuthorizationByTelegramID: make(map[int64]struct{}),
	}
}

// HandleMessage is the default handler for all incoming Telegram updates.
// It verifies access before echoing messages from authorized users.
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
		telegramHandler.sendText(ctx, telegramBot, chatID, messageThreadID, incomingText)
	}
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
