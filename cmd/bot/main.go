package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tourplannerbot/internal/config"
	"tourplannerbot/internal/database"
	"tourplannerbot/internal/telegram"

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

	messageHandler := telegram.NewHandler(logger, databaseConnection, applicationConfig.AccessPIN)

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
