package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tourplannerbot/internal/config"
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

	logger.Info("starting tourplannerbot")

	messageHandler := telegram.NewHandler(logger)

	telegramBot, err := bot.New(applicationConfig.TelegramBotToken,
		bot.WithDefaultHandler(messageHandler.HandleMessage),
	)
	if err != nil {
		logger.Error("failed to create telegram bot", "error", err)
		os.Exit(1)
	}

	ctx, cancelContext := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelContext()

	logger.Info("bot is running, press Ctrl+C to stop")
	telegramBot.Start(ctx)
	logger.Info("bot stopped gracefully")
}
