package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tourplannerbot/internal/llm"
	applicationModels "tourplannerbot/internal/models"
	applicationTools "tourplannerbot/internal/tools"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"gorm.io/gorm"
)

// responseGenerator produces one text response from conversation context.
type responseGenerator interface {
	Generate(ctx context.Context, instructions string, conversationMessages []llm.Message, toolDefinitions []applicationTools.Definition) (llm.Generation, error)
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
	systemInstructions                  string
	messageHistoryMaxMessages           int
	toolCallMaxIterations               int
	toolRegistry                        *applicationTools.Registry
	pendingAuthorizationByTelegramID    map[int64]struct{}
	pendingAuthorizationByTelegramMutex sync.Mutex
}

// NewHandler creates a message handler with the supplied system instructions and history limit.
func NewHandler(logger *slog.Logger, databaseConnection *gorm.DB, accessPIN string, systemInstructions string, messageHistoryMaxMessages int, toolCallMaxIterations int, toolRegistry *applicationTools.Registry, responseGenerator responseGenerator) *Handler {
	return &Handler{
		logger:                           logger,
		databaseConnection:               databaseConnection,
		responseGenerator:                responseGenerator,
		accessPIN:                        accessPIN,
		systemInstructions:               systemInstructions,
		messageHistoryMaxMessages:        messageHistoryMaxMessages,
		toolCallMaxIterations:            toolCallMaxIterations,
		toolRegistry:                     toolRegistry,
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
		userMessage, err := telegramHandler.saveUserMessage(ctx, chatID, messageThreadID, senderUser.ID, incomingText)
		if err != nil {
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

		responseProgress := newTelegramResponseProgress(ctx, telegramHandler, telegramBot, chatID, messageThreadID)
		responseText, err := telegramHandler.generateResponseWithTools(ctx, userMessage.ID, chatID, messageThreadID, senderUser.ID, conversationMessages, responseProgress)
		if err != nil {
			telegramHandler.logger.Error("failed to generate LLM response",
				"chat_id", chatID,
				"telegram_user_id", senderUser.ID,
				"error", err,
			)
			responseProgress.finish(ctx, "No he pogut generar la resposta. Torna-ho a provar.")
			return
		}
		responseProgress.finish(ctx, responseText)
	}
}

// generateResponseWithTools runs the bounded LLM/tool loop, persists every tool
// call and result, and returns the final assistant text.
func (telegramHandler *Handler) generateResponseWithTools(ctx context.Context, sourceMessageID uint64, chatID int64, messageThreadID int, userID int64, conversationMessages []llm.Message, responseProgress responseProgressReporter) (string, error) {
	if telegramHandler.toolCallMaxIterations < 1 {
		return "", fmt.Errorf("tool call iteration limit must be positive")
	}

	toolDefinitions := telegramHandler.toolRegistry.Definitions()
	for iteration := 0; iteration < telegramHandler.toolCallMaxIterations; iteration++ {
		generation, generationError := telegramHandler.responseGenerator.Generate(ctx, telegramHandler.systemInstructions, conversationMessages, toolDefinitions)
		if loggingError := telegramHandler.saveLLMRequest(ctx, sourceMessageID, chatID, messageThreadID, userID, generation, generationError); loggingError != nil {
			telegramHandler.logger.Error("failed to save LLM request audit record",
				"chat_id", chatID,
				"message_thread_id", messageThreadID,
				"tool_iteration", iteration,
				"error", loggingError,
			)
		}
		if generationError != nil {
			return "", generationError
		}

		if len(generation.ToolCalls) == 0 {
			if generation.Text == "" {
				return "", fmt.Errorf("LLM returned neither text nor tool calls")
			}
			if err := telegramHandler.saveAssistantMessage(ctx, chatID, messageThreadID, generation.Text); err != nil {
				return "", fmt.Errorf("save final assistant message: %w", err)
			}
			return generation.Text, nil
		}

		continuationMessages := generation.ContinuationMessages
		if len(continuationMessages) == 0 {
			for _, toolCall := range generation.ToolCalls {
				continuationMessages = append(continuationMessages, llm.Message{
					Role:          applicationModels.MessageRoleAssistant,
					ToolCallID:    toolCall.ID,
					ToolName:      toolCall.Name,
					ToolArguments: toolCall.Arguments,
				})
			}
		}
		for _, continuationMessage := range continuationMessages {
			switch continuationMessage.Role {
			case applicationModels.MessageRoleReasoning:
				if err := telegramHandler.saveReasoningMessage(ctx, chatID, messageThreadID, continuationMessage.Content); err != nil {
					return "", fmt.Errorf("save reasoning continuation: %w", err)
				}
			case applicationModels.MessageRoleAssistant:
				toolCall := llm.ToolCall{
					ID:        continuationMessage.ToolCallID,
					Name:      continuationMessage.ToolName,
					Arguments: continuationMessage.ToolArguments,
				}
				if err := telegramHandler.saveAssistantToolCall(ctx, chatID, messageThreadID, toolCall); err != nil {
					return "", fmt.Errorf("save assistant tool call %q: %w", toolCall.ID, err)
				}
			default:
				return "", fmt.Errorf("unsupported LLM continuation role %q", continuationMessage.Role)
			}
			conversationMessages = append(conversationMessages, continuationMessage)
		}

		for _, toolCall := range generation.ToolCalls {
			responseProgress.reportToolUse(ctx, toolCall.Name, telegramHandler.toolRegistry.Source(toolCall.Name))
			toolExecutionStartedAt := time.Now()
			telegramHandler.logger.Info(fmt.Sprintf("using tool %s", toolCall.Name),
				"chat_id", chatID,
				"message_thread_id", messageThreadID,
				"tool_iteration", iteration,
				"tool_name", toolCall.Name,
				"tool_call_id", toolCall.ID,
			)
			toolResult, toolError := telegramHandler.toolRegistry.Execute(ctx, toolCall.Name, json.RawMessage(toolCall.Arguments))
			toolExecutionDuration := time.Since(toolExecutionStartedAt)
			if toolError != nil {
				toolResult = encodeToolError(toolError)
				telegramHandler.logger.Warn(fmt.Sprintf("tool %s use failed after %s", toolCall.Name, toolExecutionDuration),
					"chat_id", chatID,
					"message_thread_id", messageThreadID,
					"tool_iteration", iteration,
					"tool_name", toolCall.Name,
					"tool_call_id", toolCall.ID,
					"duration_ms", toolExecutionDuration.Milliseconds(),
					"status", "failed",
					"error", toolError,
				)
			} else {
				telegramHandler.logger.Info(fmt.Sprintf("tool %s use completed in %s", toolCall.Name, toolExecutionDuration),
					"chat_id", chatID,
					"message_thread_id", messageThreadID,
					"tool_iteration", iteration,
					"tool_name", toolCall.Name,
					"tool_call_id", toolCall.ID,
					"duration_ms", toolExecutionDuration.Milliseconds(),
					"status", "succeeded",
					"result_length", len(toolResult),
				)
			}
			if err := telegramHandler.saveToolResult(ctx, chatID, messageThreadID, toolCall, toolResult); err != nil {
				return "", fmt.Errorf("save result for tool call %q: %w", toolCall.ID, err)
			}
			conversationMessages = append(conversationMessages, llm.Message{
				Role:       applicationModels.MessageRoleTool,
				Content:    toolResult,
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.Name,
			})
		}
		responseProgress.reportPreparingResponse(ctx)
	}

	return "", fmt.Errorf("tool call loop exceeded %d iterations", telegramHandler.toolCallMaxIterations)
}

// encodeToolError returns a stable JSON result that the model can interpret.
func encodeToolError(toolError error) string {
	encodedError, err := json.Marshal(map[string]string{"error": toolError.Error()})
	if err != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(encodedError)
}

// saveUserMessage persists an authorized user's text in its Telegram conversation.
func (telegramHandler *Handler) saveUserMessage(ctx context.Context, chatID int64, messageThreadID int, userID int64, content string) (*applicationModels.Message, error) {
	message := &applicationModels.Message{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		UserID:          &userID,
		Role:            applicationModels.MessageRoleUser,
		Content:         content,
	}
	if err := telegramHandler.databaseConnection.WithContext(ctx).Create(message).Error; err != nil {
		return nil, err
	}
	return message, nil
}

// saveLLMRequest persists an LLM call's metadata without duplicating its text.
func (telegramHandler *Handler) saveLLMRequest(ctx context.Context, sourceMessageID uint64, chatID int64, messageThreadID int, userID int64, generation llm.Generation, generationError error) error {
	status := applicationModels.LLMRequestStatusSucceeded
	if generationError != nil {
		status = applicationModels.LLMRequestStatusFailed
	}

	auditRecord := applicationModels.LLMRequest{
		SourceMessageID:       &sourceMessageID,
		ChatID:                chatID,
		MessageThreadID:       messageThreadID,
		TelegramUserID:        &userID,
		Provider:              generation.Provider,
		Model:                 generation.Model,
		Operation:             "response",
		Status:                status,
		DurationMS:            int(generation.Duration.Milliseconds()),
		EstimatedCostMicroUSD: generation.EstimatedCostMicroUSD,
	}
	if generation.ProviderResponseID != "" {
		auditRecord.ProviderResponseID = &generation.ProviderResponseID
	}
	if generation.PricingVersion != "" {
		auditRecord.PricingVersion = &generation.PricingVersion
	}
	if generation.UsageAvailable {
		auditRecord.InputTokens = &generation.InputTokens
		auditRecord.CachedInputTokens = &generation.CachedInputTokens
		auditRecord.OutputTokens = &generation.OutputTokens
		auditRecord.ReasoningTokens = &generation.ReasoningTokens
		auditRecord.TotalTokens = &generation.TotalTokens
	} else if generationError != nil {
		errorMessage := generationError.Error()
		auditRecord.ErrorMessage = &errorMessage
	}

	return telegramHandler.databaseConnection.WithContext(ctx).Create(&auditRecord).Error
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

// saveAssistantToolCall persists one function call requested by the model.
func (telegramHandler *Handler) saveAssistantToolCall(ctx context.Context, chatID int64, messageThreadID int, toolCall llm.ToolCall) error {
	return telegramHandler.databaseConnection.WithContext(ctx).Create(&applicationModels.Message{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Role:            applicationModels.MessageRoleAssistant,
		Content:         "",
		ToolCallID:      stringPointer(toolCall.ID),
		ToolName:        stringPointer(toolCall.Name),
		ToolArguments:   stringPointer(toolCall.Arguments),
	}).Error
}

// saveReasoningMessage persists encrypted provider state needed to continue a
// stateless tool-calling response.
func (telegramHandler *Handler) saveReasoningMessage(ctx context.Context, chatID int64, messageThreadID int, content string) error {
	return telegramHandler.databaseConnection.WithContext(ctx).Create(&applicationModels.Message{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Role:            applicationModels.MessageRoleReasoning,
		Content:         content,
	}).Error
}

// saveToolResult persists the output paired with one model tool call.
func (telegramHandler *Handler) saveToolResult(ctx context.Context, chatID int64, messageThreadID int, toolCall llm.ToolCall, result string) error {
	return telegramHandler.databaseConnection.WithContext(ctx).Create(&applicationModels.Message{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Role:            applicationModels.MessageRoleTool,
		Content:         result,
		ToolCallID:      stringPointer(toolCall.ID),
		ToolName:        stringPointer(toolCall.Name),
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
		conversationMessage := llm.Message{
			Role:    persistedMessage.Role,
			Content: persistedMessage.Content,
		}
		if persistedMessage.ToolCallID != nil {
			conversationMessage.ToolCallID = *persistedMessage.ToolCallID
		}
		if persistedMessage.ToolName != nil {
			conversationMessage.ToolName = *persistedMessage.ToolName
		}
		if persistedMessage.ToolArguments != nil {
			conversationMessage.ToolArguments = *persistedMessage.ToolArguments
		}
		conversationMessages = append(conversationMessages, conversationMessage)
	}
	for len(conversationMessages) > 0 && conversationMessages[0].Role == applicationModels.MessageRoleTool {
		conversationMessages = conversationMessages[1:]
	}

	return conversationMessages, nil
}

// stringPointer returns a pointer suitable for nullable persistence fields.
func stringPointer(value string) *string {
	return &value
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

// sendText sends text to a Telegram chat, logs delivery failures, and returns
// the final Telegram API error when no delivery format succeeds.
func (telegramHandler *Handler) sendText(ctx context.Context, telegramBot *bot.Bot, chatID int64, messageThreadID int, text string) error {
	formattedRichHTML, hasTables := formatTelegramRichHTML(text)
	if hasTables {
		_, richMessageError := telegramBot.SendRichMessage(ctx, &bot.SendRichMessageParams{
			ChatID:          chatID,
			MessageThreadID: messageThreadID,
			RichMessage: models.InputRichMessage{
				HTML: formattedRichHTML,
			},
		})
		if richMessageError == nil {
			return nil
		}
		telegramHandler.logger.Warn("Telegram rich table failed, using readable list fallback",
			"chat_id", chatID,
			"message_thread_id", messageThreadID,
			"error", richMessageError,
		)
	}

	_, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Text:            formatTelegramHTML(text),
		ParseMode:       models.ParseModeHTML,
	})
	if err != nil {
		telegramHandler.logger.Error("failed to send Telegram message",
			"chat_id", chatID,
			"message_thread_id", messageThreadID,
			"error", err,
		)
	}
	return err
}
