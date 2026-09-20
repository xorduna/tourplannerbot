package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	applicationModels "tourplannerbot/internal/models"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const typingRefreshInterval = 4 * time.Second

// responseProgressReporter receives user-visible milestones from the LLM/tool
// loop without coupling the loop to one transport implementation.
type responseProgressReporter interface {
	reportToolUse(ctx context.Context, toolName string, toolSource string)
	reportPreparingResponse(ctx context.Context)
}

// telegramResponseProgress owns the temporary Telegram status message and the
// periodically refreshed typing indicator for one incoming user message.
type telegramResponseProgress struct {
	handler           *Handler
	telegramBot       *bot.Bot
	chatID            int64
	messageThreadID   int
	statusMessageID   int
	typingCancel      context.CancelFunc
	typingFinished    chan struct{}
	stopTypingOnce    sync.Once
	statusTextMutex   sync.Mutex
	currentStatusText string
}

// newTelegramResponseProgress posts the initial thinking message and starts a
// background typing indicator. Telegram delivery errors never abort generation.
func newTelegramResponseProgress(ctx context.Context, handler *Handler, telegramBot *bot.Bot, chatID int64, messageThreadID int) *telegramResponseProgress {
	progress := &telegramResponseProgress{
		handler:           handler,
		telegramBot:       telegramBot,
		chatID:            chatID,
		messageThreadID:   messageThreadID,
		currentStatusText: "💭 Pensant…",
	}

	statusMessage, statusMessageError := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Text:            progress.currentStatusText,
	})
	if statusMessageError != nil {
		handler.logger.Warn("failed to send Telegram response progress message",
			"chat_id", chatID,
			"message_thread_id", messageThreadID,
			"error", statusMessageError,
		)
	} else {
		progress.statusMessageID = statusMessage.ID
	}

	typingContext, typingCancel := context.WithCancel(ctx)
	progress.typingCancel = typingCancel
	progress.typingFinished = make(chan struct{})
	progress.sendTypingAction(typingContext)
	go progress.refreshTypingAction(typingContext)

	return progress
}

// reportToolUse replaces the thinking text with a concise, privacy-preserving
// description of the tool currently being used.
func (progress *telegramResponseProgress) reportToolUse(ctx context.Context, toolName string, toolSource string) {
	progress.updateStatus(ctx, toolProgressText(toolName, toolSource))
}

// reportPreparingResponse tells the user that completed tool output is being
// turned into the final answer by the model.
func (progress *telegramResponseProgress) reportPreparingResponse(ctx context.Context) {
	progress.updateStatus(ctx, "✍️ Preparant la resposta…")
}

// finish stops the typing indicator and replaces the temporary status with the
// final response. If editing fails, it sends a new message and removes the stale
// placeholder after successful delivery.
func (progress *telegramResponseProgress) finish(ctx context.Context, responseText string) {
	progress.stopTyping()
	if progress.statusMessageID == 0 {
		_ = progress.handler.sendText(ctx, progress.telegramBot, progress.chatID, progress.messageThreadID, responseText)
		return
	}

	if editError := progress.editFinalResponse(ctx, responseText); editError == nil {
		return
	} else {
		progress.handler.logger.Warn("failed to replace Telegram progress message with final response",
			"chat_id", progress.chatID,
			"message_thread_id", progress.messageThreadID,
			"message_id", progress.statusMessageID,
			"error", editError,
		)
	}

	if sendError := progress.handler.sendText(ctx, progress.telegramBot, progress.chatID, progress.messageThreadID, responseText); sendError != nil {
		return
	}
	if _, deleteError := progress.telegramBot.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    progress.chatID,
		MessageID: progress.statusMessageID,
	}); deleteError != nil {
		progress.handler.logger.Warn("failed to remove stale Telegram progress message",
			"chat_id", progress.chatID,
			"message_thread_id", progress.messageThreadID,
			"message_id", progress.statusMessageID,
			"error", deleteError,
		)
	}
}

// finishDraftPreview replaces the temporary status with the complete separated
// draft preview and its Mini App button. It returns the delivered Telegram
// message ID so the caller can retain it for future preview synchronization.
func (progress *telegramResponseProgress) finishDraftPreview(ctx context.Context, chatType models.ChatType, draft *applicationModels.Draft, responseText string) *int64 {
	progress.stopTyping()
	heading := strings.TrimSpace(responseText)
	if heading == "" {
		heading = "T’he preparat aquesta proposta."
	}

	if progress.statusMessageID != 0 {
		_, editError := progress.telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      progress.chatID,
			MessageID:   progress.statusMessageID,
			Text:        formatTelegramHTML(draftPreviewText(heading, draft)),
			ParseMode:   models.ParseModeHTML,
			ReplyMarkup: progress.handler.draftPreviewReplyMarkup(chatType, draft.ID),
		})
		if editError == nil {
			telegramMessageID := int64(progress.statusMessageID)
			return &telegramMessageID
		}
		progress.handler.logger.Warn("failed to replace Telegram progress message with draft preview",
			"chat_id", progress.chatID,
			"message_thread_id", progress.messageThreadID,
			"message_id", progress.statusMessageID,
			"draft_id", draft.ID,
			"error", editError,
		)
	}

	previewMessage, sendError := progress.handler.sendDraftPreviewMessage(ctx, progress.telegramBot, progress.chatID, progress.messageThreadID, chatType, draft, heading)
	if sendError != nil {
		progress.handler.logger.Error("failed to send Telegram draft preview", "chat_id", progress.chatID, "draft_id", draft.ID, "error", sendError)
		return nil
	}
	if progress.statusMessageID != 0 {
		if _, deleteError := progress.telegramBot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    progress.chatID,
			MessageID: progress.statusMessageID,
		}); deleteError != nil {
			progress.handler.logger.Warn("failed to remove stale Telegram progress message after draft preview delivery",
				"chat_id", progress.chatID,
				"message_thread_id", progress.messageThreadID,
				"message_id", progress.statusMessageID,
				"error", deleteError,
			)
		}
	}
	telegramMessageID := int64(previewMessage.ID)
	return &telegramMessageID
}

// editFinalResponse preserves native Rich Message tables when the response has
// one and otherwise edits the placeholder as ordinary Telegram HTML.
func (progress *telegramResponseProgress) editFinalResponse(ctx context.Context, responseText string) error {
	formattedRichHTML, hasTables := formatTelegramRichHTML(responseText)
	editParameters := &bot.EditMessageTextParams{
		ChatID:    progress.chatID,
		MessageID: progress.statusMessageID,
	}
	if hasTables {
		editParameters.RichMessage = &models.InputRichMessage{HTML: formattedRichHTML}
	} else {
		editParameters.Text = formatTelegramHTML(responseText)
		editParameters.ParseMode = models.ParseModeHTML
	}
	_, editError := progress.telegramBot.EditMessageText(ctx, editParameters)
	return editError
}

// updateStatus edits the temporary message only when the visible text changes.
// A failed edit is logged but does not interrupt the LLM or tool call.
func (progress *telegramResponseProgress) updateStatus(ctx context.Context, statusText string) {
	progress.statusTextMutex.Lock()
	defer progress.statusTextMutex.Unlock()
	if progress.statusMessageID == 0 || statusText == progress.currentStatusText {
		return
	}

	_, updateError := progress.telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    progress.chatID,
		MessageID: progress.statusMessageID,
		Text:      statusText,
	})
	if updateError != nil {
		progress.handler.logger.Warn("failed to update Telegram response progress message",
			"chat_id", progress.chatID,
			"message_thread_id", progress.messageThreadID,
			"message_id", progress.statusMessageID,
			"status_text", statusText,
			"error", updateError,
		)
		return
	}
	progress.currentStatusText = statusText
	progress.sendTypingAction(ctx)
}

// refreshTypingAction refreshes Telegram's short-lived typing state until the
// final response is ready or the incoming request context is cancelled.
func (progress *telegramResponseProgress) refreshTypingAction(ctx context.Context) {
	defer close(progress.typingFinished)
	typingTicker := time.NewTicker(typingRefreshInterval)
	defer typingTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-typingTicker.C:
			progress.sendTypingAction(ctx)
		}
	}
}

// sendTypingAction emits one typing indicator and treats delivery as optional
// presentation feedback rather than a response-generation failure.
func (progress *telegramResponseProgress) sendTypingAction(ctx context.Context) {
	_, typingError := progress.telegramBot.SendChatAction(ctx, &bot.SendChatActionParams{
		ChatID:          progress.chatID,
		MessageThreadID: progress.messageThreadID,
		Action:          models.ChatActionTyping,
	})
	if typingError != nil && ctx.Err() == nil {
		progress.handler.logger.Debug("failed to send Telegram typing action",
			"chat_id", progress.chatID,
			"message_thread_id", progress.messageThreadID,
			"error", typingError,
		)
	}
}

// stopTyping cancels the refresh loop and waits for its goroutine to finish.
func (progress *telegramResponseProgress) stopTyping() {
	progress.stopTypingOnce.Do(func() {
		progress.typingCancel()
		<-progress.typingFinished
	})
}

// toolProgressText turns internal tool names into short Catalan activity text.
// It intentionally excludes arguments so user queries and coordinates are not
// copied into transient status updates or their error logs.
func toolProgressText(toolName string, toolSource string) string {
	normalizedToolName := strings.ToLower(toolName)
	normalizedToolSource := strings.ToLower(toolSource)
	switch {
	case normalizedToolName == "current_time":
		return "🕐 Consultant l’hora…"
	case normalizedToolName == "create_draft":
		return "📝 Preparant la proposta…"
	case normalizedToolName == "update_draft":
		return "📝 Actualitzant la proposta…"
	case normalizedToolSource == "openstreetmap" || strings.Contains(normalizedToolName, "openstreetmap"):
		return fmt.Sprintf("🗺️ Utilitzant OpenStreetMap per %s…", openStreetMapToolPurpose(normalizedToolName))
	case normalizedToolSource == "wikipedia" || strings.Contains(normalizedToolName, "wikipedia") || isWikipediaToolName(normalizedToolName):
		return fmt.Sprintf("🔎 Utilitzant Wikipedia per %s…", wikipediaToolPurpose(normalizedToolName))
	default:
		return fmt.Sprintf("🛠️ Utilitzant %s…", toolName)
	}
}

// openStreetMapToolPurpose describes known map operations without their input.
func openStreetMapToolPurpose(toolName string) string {
	switch {
	case strings.Contains(toolName, "reverse_geocode"):
		return "identificar una ubicació"
	case strings.Contains(toolName, "nearby"):
		return "buscar llocs propers"
	case strings.Contains(toolName, "bbox"):
		return "consultar aquesta zona"
	case strings.Contains(toolName, "lookup"):
		return "consultar elements del mapa"
	case strings.Contains(toolName, "search"):
		return "buscar llocs"
	default:
		return "consultar el mapa"
	}
}

// wikipediaToolPurpose describes known encyclopedia operations without their input.
func wikipediaToolPurpose(toolName string) string {
	switch {
	case strings.Contains(toolName, "summary") || strings.Contains(toolName, "summarize"):
		return "obtenir un resum"
	case strings.Contains(toolName, "coordinate"):
		return "obtenir les coordenades"
	case strings.Contains(toolName, "section"):
		return "consultar les seccions"
	case strings.Contains(toolName, "link"):
		return "consultar els enllaços"
	case strings.Contains(toolName, "related"):
		return "buscar temes relacionats"
	case strings.Contains(toolName, "article"):
		return "consultar un article"
	case strings.Contains(toolName, "search"):
		return "buscar informació"
	default:
		return "consultar informació"
	}
}

// isWikipediaToolName recognizes unprefixed tools advertised by the local
// Wikipedia MCP server.
func isWikipediaToolName(toolName string) bool {
	switch toolName {
	case "get_article", "get_summary", "summarize_article_for_query", "summarize_article_section", "extract_key_facts", "get_related_topics", "get_sections", "get_links", "get_coordinates":
		return true
	default:
		return false
	}
}
