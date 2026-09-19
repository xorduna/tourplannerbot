package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"tourplannerbot/internal/llm"

	"github.com/spf13/viper"
)

const defaultCurrentTimeTimezone = "Europe/Madrid"

// CurrentTimeToolConfig contains configuration for the native current-time tool.
type CurrentTimeToolConfig struct {
	Enabled         bool
	DefaultTimezone string
}

// ToolsConfig contains configuration shared by the application's tools.
type ToolsConfig struct {
	CurrentTime CurrentTimeToolConfig
}

// Config holds all application configuration values.
type Config struct {
	TelegramBotToken      string
	DatabaseURL           string
	OpenAIAPIKey          string
	OpenAIModel           string
	OpenAIBaseURL         string
	LLMProvider           string
	LLMTariffsDirectory   string
	LLMMaxTokens          int
	LLMPricing            *llm.Pricing
	LLMHistoryMaxMessages int
	ToolCallMaxIterations int
	Tools                 ToolsConfig
	AccessPIN             string
	LogLevel              string
}

// LoadFromEnvironment reads application configuration through Viper. Environment
// variables use uppercase names with underscores, such as OPENAI_API_KEY and
// TOOLS_CURRENT_TIME_ENABLED.
func LoadFromEnvironment() (*Config, error) {
	configuration := viper.New()
	configuration.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	configuration.AutomaticEnv()

	configuration.SetDefault("openai.model", "gpt-5.5")
	configuration.SetDefault("openai.base_url", "https://api.openai.com/v1")
	configuration.SetDefault("llm.provider", "openai")
	configuration.SetDefault("llm.tariffs_dir", "tariffs")
	configuration.SetDefault("llm.max_tokens", 2048)
	configuration.SetDefault("llm.history_max_messages", 20)
	configuration.SetDefault("tool_call_max_iterations", 10)
	configuration.SetDefault("tools.current_time.enabled", true)
	configuration.SetDefault("tools.current_time.default_timezone", defaultCurrentTimeTimezone)
	configuration.SetDefault("log_level", "info")

	telegramBotToken, err := requiredString(configuration, "telegram_bot_token")
	if err != nil {
		return nil, err
	}
	databaseURL, err := requiredString(configuration, "database_url")
	if err != nil {
		return nil, err
	}
	openAIAPIKey, err := requiredString(configuration, "openai_api_key")
	if err != nil {
		return nil, err
	}
	accessPIN, err := requiredString(configuration, "access_pin")
	if err != nil {
		return nil, err
	}

	llmMaxTokens, err := positiveInteger(configuration, "llm.max_tokens", "LLM_MAX_TOKENS")
	if err != nil {
		return nil, err
	}
	llmHistoryMaxMessages, err := positiveInteger(configuration, "llm.history_max_messages", "LLM_HISTORY_MAX_MESSAGES")
	if err != nil {
		return nil, err
	}
	toolCallMaxIterations, err := positiveInteger(configuration, "tool_call_max_iterations", "TOOL_CALL_MAX_ITERATIONS")
	if err != nil {
		return nil, err
	}
	currentTimeEnabled, err := booleanValue(configuration, "tools.current_time.enabled", "TOOLS_CURRENT_TIME_ENABLED")
	if err != nil {
		return nil, err
	}
	currentTimeDefaultTimezone := strings.TrimSpace(configuration.GetString("tools.current_time.default_timezone"))
	if _, err := time.LoadLocation(currentTimeDefaultTimezone); err != nil {
		return nil, fmt.Errorf("TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE must be a valid IANA timezone, got %q: %w", currentTimeDefaultTimezone, err)
	}

	openAIModel := configuration.GetString("openai.model")
	llmProvider := configuration.GetString("llm.provider")
	llmTariffsDirectory := configuration.GetString("llm.tariffs_dir")
	llmPricing, err := llm.LoadPricing(llmTariffsDirectory, llmProvider, openAIModel)
	if err != nil {
		return nil, err
	}

	return &Config{
		TelegramBotToken:      telegramBotToken,
		DatabaseURL:           databaseURL,
		OpenAIAPIKey:          openAIAPIKey,
		OpenAIModel:           openAIModel,
		OpenAIBaseURL:         configuration.GetString("openai.base_url"),
		LLMProvider:           llmProvider,
		LLMTariffsDirectory:   llmTariffsDirectory,
		LLMMaxTokens:          llmMaxTokens,
		LLMPricing:            llmPricing,
		LLMHistoryMaxMessages: llmHistoryMaxMessages,
		ToolCallMaxIterations: toolCallMaxIterations,
		Tools: ToolsConfig{
			CurrentTime: CurrentTimeToolConfig{
				Enabled:         currentTimeEnabled,
				DefaultTimezone: currentTimeDefaultTimezone,
			},
		},
		AccessPIN: accessPIN,
		LogLevel:  configuration.GetString("log_level"),
	}, nil
}

// requiredString returns a non-empty required configuration value.
func requiredString(configuration *viper.Viper, key string) (string, error) {
	value := strings.TrimSpace(configuration.GetString(key))
	if value == "" {
		return "", fmt.Errorf("%s environment variable is required", strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
	}
	return value, nil
}

// positiveInteger parses and validates a positive integer configuration value.
func positiveInteger(configuration *viper.Viper, key string, environmentVariableName string) (int, error) {
	rawValue := configuration.GetString(key)
	parsedValue, err := strconv.Atoi(rawValue)
	if err != nil || parsedValue < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got: %s", environmentVariableName, rawValue)
	}
	return parsedValue, nil
}

// booleanValue parses a boolean configuration value without silently accepting
// invalid strings.
func booleanValue(configuration *viper.Viper, key string, environmentVariableName string) (bool, error) {
	rawValue := configuration.GetString(key)
	parsedValue, err := strconv.ParseBool(rawValue)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got: %s", environmentVariableName, rawValue)
	}
	return parsedValue, nil
}
