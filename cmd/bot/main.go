package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tourplannerbot/internal/config"
	"tourplannerbot/internal/database"
	"tourplannerbot/internal/llm"
	"tourplannerbot/internal/prompt"
	"tourplannerbot/internal/telegram"
	applicationTools "tourplannerbot/internal/tools"
	"tourplannerbot/internal/tools/currenttime"

	"github.com/go-telegram/bot"
)

func main() {
	applicationConfig, err := config.LoadFromEnvironment()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if applicationConfig.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	logger.Info("starting tourplannerbot", "log_level", applicationConfig.LogLevel)

	toolRegistry := applicationTools.NewRegistry()
	if applicationConfig.Tools.CurrentTime.Enabled {
		currentTimeTool, err := currenttime.New(applicationConfig.Tools.CurrentTime.DefaultTimezone)
		if err != nil {
			logger.Error("failed to initialize current_time tool", "error", err)
			os.Exit(1)
		}
		if err := toolRegistry.Register(currentTimeTool); err != nil {
			logger.Error("failed to register current_time tool", "error", err)
			os.Exit(1)
		}
	}
	registeredToolDefinitions := toolRegistry.Definitions()
	registeredToolNames := make([]string, 0, len(registeredToolDefinitions))
	for _, registeredToolDefinition := range registeredToolDefinitions {
		registeredToolNames = append(registeredToolNames, registeredToolDefinition.Name)
	}
	logger.Info("tools initialized", "enabled_tools", registeredToolNames)

	applicationContext, cancelApplicationContext := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelApplicationContext()

	databaseConnection, err := database.Open(applicationContext, applicationConfig.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	sqlDatabaseConnection, err := databaseConnection.DB()
	if err != nil {
		logger.Error("failed to access database connection pool", "error", err)
		os.Exit(1)
	}
	defer sqlDatabaseConnection.Close()

	llmClient := llm.NewClient(
		applicationConfig.OpenAIAPIKey,
		applicationConfig.OpenAIBaseURL,
		applicationConfig.LLMProvider,
		applicationConfig.OpenAIModel,
		applicationConfig.LLMMaxTokens,
		applicationConfig.LLMPricing,
	)
	systemInstructions, err := prompt.Load("prompts/system_query.md")
	if err != nil {
		logger.Error("failed to load query system prompt", "error", err)
		os.Exit(1)
	}
	messageHandler := telegram.NewHandler(
		logger,
		databaseConnection,
		applicationConfig.AccessPIN,
		systemInstructions,
		applicationConfig.LLMHistoryMaxMessages,
		applicationConfig.ToolCallMaxIterations,
		toolRegistry,
		llmClient,
	)

	telegramBot, err := bot.New(applicationConfig.TelegramBotToken,
		bot.WithDefaultHandler(messageHandler.HandleMessage),
	)
	if err != nil {
		logger.Error("failed to create telegram bot", "error", err)
		os.Exit(1)
	}

	botInfo, err := telegramBot.GetMe(applicationContext)
	if err != nil {
		logger.Error("failed to get bot info from telegram", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to telegram",
		"bot_id", botInfo.ID,
		"bot_username", botInfo.Username,
		"bot_name", botInfo.FirstName,
		"can_join_groups", botInfo.CanJoinGroups,
		"can_read_all_group_messages", botInfo.CanReadAllGroupMessages,
	)

	logger.Info("bot is running, press Ctrl+C to stop")
	telegramBot.Start(applicationContext)
	logger.Info("bot stopped gracefully")
}
