package telegram

import (
	"context"
	"errors"
	"fmt"

	applicationModels "tourplannerbot/internal/models"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const updatedDraftPreviewHeading = "He actualitzat aquesta proposta."

// DraftPreviewUpdater updates the saved Telegram preview after a Mini App
// editor save.
type DraftPreviewUpdater struct {
	telegramBot *bot.Bot
	handler     *Handler
}

// NewDraftPreviewUpdater creates the Telegram-side implementation used by the
// Mini App draft store after the bot has connected.
func NewDraftPreviewUpdater(telegramBot *bot.Bot, handler *Handler) *DraftPreviewUpdater {
	return &DraftPreviewUpdater{telegramBot: telegramBot, handler: handler}
}

// UpdateDraftPreview replaces the preview text with the canonical saved draft.
func (draftPreviewUpdater *DraftPreviewUpdater) UpdateDraftPreview(ctx context.Context, draft *applicationModels.Draft) error {
	if draft == nil || draft.TelegramMessageID == nil {
		return nil
	}
	if draftPreviewUpdater.telegramBot == nil {
		return errors.New("Telegram bot is unavailable")
	}
	if draftPreviewUpdater.handler == nil {
		return errors.New("Telegram draft preview handler is unavailable")
	}
	chat, err := draftPreviewUpdater.telegramBot.GetChat(ctx, &bot.GetChatParams{ChatID: draft.ChatID})
	if err != nil {
		return fmt.Errorf("get draft chat: %w", err)
	}
	replyMarkup := draftPreviewUpdater.handler.draftPreviewReplyMarkup(chat.Type, draft.ID)
	if replyMarkup == nil {
		return errors.New("Telegram draft editor link is unavailable")
	}
	_, err = draftPreviewUpdater.telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:             draft.ChatID,
		MessageID:          int(*draft.TelegramMessageID),
		Text:               formatTelegramHTML(draftPreviewText(updatedDraftPreviewHeading, draft)),
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: disabledLinkPreview(),
		ReplyMarkup:        replyMarkup,
	})
	return err
}
