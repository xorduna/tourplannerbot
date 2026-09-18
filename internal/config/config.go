package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration values loaded from environment variables.
type Config struct {
	TelegramBotToken      string
	DatabaseURL           string
	OpenAIAPIKey          string
	OpenAIModel           string
	OpenAIBaseURL         string
	LLMMaxTokens          int
	LLMHistoryMaxMessages int
	ToolCallMaxIterations int
	AccessPIN             string
	LogLevel              string
}

// LoadFromEnvironment reads all required configuration from environment variables
// and returns a populated Config struct or an error if required values are missing.
func LoadFromEnvironment() (*Config, error) {
	telegramBotToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN environment variable is required")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	openAIAPIKey := os.Getenv("OPENAI_API_KEY")
	if openAIAPIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
	}

	accessPIN := os.Getenv("ACCESS_PIN")
	if accessPIN == "" {
		return nil, fmt.Errorf("ACCESS_PIN environment variable is required")
	}

	openAIModel := os.Getenv("OPENAI_MODEL")
	if openAIModel == "" {
		openAIModel = "gpt-5.5"
	}

	openAIBaseURL := os.Getenv("OPENAI_BASE_URL")
	if openAIBaseURL == "" {
		openAIBaseURL = "https://api.openai.com/v1"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	llmMaxTokens := 2048
	if rawValue := os.Getenv("LLM_MAX_TOKENS"); rawValue != "" {
		parsedValue, err := strconv.Atoi(rawValue)
		if err != nil {
			return nil, fmt.Errorf("LLM_MAX_TOKENS must be an integer, got: %s", rawValue)
		}
		llmMaxTokens = parsedValue
	}

	llmHistoryMaxMessages := 20
	if rawValue := os.Getenv("LLM_HISTORY_MAX_MESSAGES"); rawValue != "" {
		parsedValue, err := strconv.Atoi(rawValue)
		if err != nil || parsedValue < 1 {
			return nil, fmt.Errorf("LLM_HISTORY_MAX_MESSAGES must be a positive integer, got: %s", rawValue)
		}
		llmHistoryMaxMessages = parsedValue
	}

	toolCallMaxIterations := 10
	if rawValue := os.Getenv("TOOL_CALL_MAX_ITERATIONS"); rawValue != "" {
		parsedValue, err := strconv.Atoi(rawValue)
		if err != nil {
			return nil, fmt.Errorf("TOOL_CALL_MAX_ITERATIONS must be an integer, got: %s", rawValue)
		}
		toolCallMaxIterations = parsedValue
	}

	return &Config{
		TelegramBotToken:      telegramBotToken,
		DatabaseURL:           databaseURL,
		OpenAIAPIKey:          openAIAPIKey,
		OpenAIModel:           openAIModel,
		OpenAIBaseURL:         openAIBaseURL,
		LLMMaxTokens:          llmMaxTokens,
		LLMHistoryMaxMessages: llmHistoryMaxMessages,
		ToolCallMaxIterations: toolCallMaxIterations,
		AccessPIN:             accessPIN,
		LogLevel:              logLevel,
	}, nil
}
