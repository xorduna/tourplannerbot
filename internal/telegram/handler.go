package telegram

import (
	"context"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Handler handles incoming Telegram messages and routes them appropriately.
type Handler struct {
	logger *slog.Logger
}

// NewHandler creates a new Handler with the provided logger.
func NewHandler(logger *slog.Logger) *Handler {
	return &Handler{
		logger: logger,
	}
}

// HandleMessage is the default handler for all incoming Telegram updates.
// In Slice 1 it simply echoes the message back to the sender.
func (telegramHandler *Handler) HandleMessage(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	incomingText := update.Message.Text
	senderUsername := update.Message.From.Username

	telegramHandler.logger.Info("received message",
		"chat_id", chatID,
		"username", senderUsername,
		"text", incomingText,
	)

	if incomingText == "" {
		return
	}

	_, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   incomingText,
	})
	if err != nil {
		telegramHandler.logger.Error("failed to send echo message",
			"chat_id", chatID,
			"error", err,
		)
	}
}
