package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"tourplannerbot/internal/llm"

	"github.com/spf13/viper"
)

const (
	defaultCurrentTimeTimezone = "Europe/Madrid"
	defaultMCPTimeout          = 30 * time.Second
	defaultBiginAccountsURL    = "https://accounts.zoho.eu"
	defaultBiginAPIURL         = "https://www.zohoapis.eu"
	defaultBiginTimeout        = 30 * time.Second
	defaultGmailOAuthURL       = "https://oauth2.googleapis.com/token"
	defaultGmailAPIURL         = "https://gmail.googleapis.com"
	defaultGmailTimeout        = 30 * time.Second
)

var (
	supportedMCPServerNames = []string{"wikipedia", "openstreetmap"}
	mcpServerNamePattern    = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// CurrentTimeToolConfig contains configuration for the native current-time tool.
type CurrentTimeToolConfig struct {
	Enabled         bool
	DefaultTimezone string
}

// BiginToolConfig contains the credentials and endpoints shared by native
// Bigin tools. The integration is enabled when all three OAuth credentials are
// configured and remains disabled when all three are empty.
type BiginToolConfig struct {
	Enabled      bool
	RefreshToken string
	ClientID     string
	ClientSecret string
	AccountsURL  string
	APIURL       string
	CallTimeout  time.Duration
}

// GmailToolConfig contains the credentials and endpoints shared by native
// Gmail tools. The integration is enabled only for a complete credential set.
type GmailToolConfig struct {
	Enabled      bool
	RefreshToken string
	ClientID     string
	ClientSecret string
	OAuthURL     string
	APIURL       string
	CallTimeout  time.Duration
}

// MCPServerConfig contains the connection and authentication settings for one
// Streamable HTTP MCP server.
type MCPServerConfig struct {
	Name        string
	Enabled     bool
	URL         string
	AuthType    string
	Token       string
	CallTimeout time.Duration
}

// ToolsConfig contains configuration shared by the application's tools.
type ToolsConfig struct {
	CurrentTime CurrentTimeToolConfig
	Bigin       BiginToolConfig
	Gmail       GmailToolConfig
	MCPServers  []MCPServerConfig
}

// Config holds all application configuration values.
type Config struct {
	Environment              string
	TelegramBotToken         string
	TelegramGroupChatID      int64
	DatabaseURL              string
	OpenAIAPIKey             string
	OpenAIModel              string
	OpenAITranscriptionModel string
	OpenAIBaseURL            string
	LLMProvider              string
	LLMTariffsDirectory      string
	LLMMaxTokens             int
	LLMPricing               *llm.Pricing
	LLMHistoryMaxMessages    int
	ToolCallMaxIterations    int
	Tools                    ToolsConfig
	AccessPIN                string
	LogLevel                 string
	Port                     int
	AppBaseURL               string
	TelegramWebAppAuthMaxAge time.Duration
}

// LoadFromEnvironment reads application configuration through Viper. Environment
// variables use uppercase names with underscores, such as OPENAI_API_KEY and
// TOOLS_CURRENT_TIME_ENABLED.
func LoadFromEnvironment() (*Config, error) {
	configuration := viper.New()
	configuration.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	configuration.AutomaticEnv()

	configuration.SetDefault("openai.model", "gpt-5.5")
	configuration.SetDefault("env", "development")
	configuration.SetDefault("openai.transcription_model", "gpt-transcribe")
	configuration.SetDefault("openai.base_url", "https://api.openai.com/v1")
	configuration.SetDefault("llm.provider", "openai")
	configuration.SetDefault("llm.tariffs_dir", "tariffs")
	configuration.SetDefault("llm.max_tokens", 2048)
	configuration.SetDefault("llm.history_max_messages", 20)
	configuration.SetDefault("tool_call_max_iterations", 10)
	configuration.SetDefault("tools.current_time.enabled", true)
	configuration.SetDefault("tools.current_time.default_timezone", defaultCurrentTimeTimezone)
	configuration.SetDefault("tools.bigin.accounts_url", defaultBiginAccountsURL)
	configuration.SetDefault("tools.bigin.api_url", defaultBiginAPIURL)
	configuration.SetDefault("tools.bigin.timeout", defaultBiginTimeout.String())
	configuration.SetDefault("tools.gmail.oauth_url", defaultGmailOAuthURL)
	configuration.SetDefault("tools.gmail.api_url", defaultGmailAPIURL)
	configuration.SetDefault("tools.gmail.timeout", defaultGmailTimeout.String())
	configuration.SetDefault("log_level", "info")
	configuration.SetDefault("port", 8080)
	configuration.SetDefault("telegram.webapp_auth_max_age", "5m")

	telegramBotToken, err := requiredString(configuration, "telegram_bot_token")
	if err != nil {
		return nil, err
	}
	telegramGroupChatID, err := privateTelegramGroupChatID(configuration.GetString("telegram_group_chat_id"))
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
	port, err := portNumber(configuration.GetString("port"))
	if err != nil {
		return nil, err
	}
	appBaseURL, err := optionalHTTPURL(configuration.GetString("app_base_url"), "APP_BASE_URL")
	if err != nil {
		return nil, err
	}
	telegramWebAppAuthMaxAge, err := positiveDuration(configuration.GetString("telegram.webapp_auth_max_age"), "TELEGRAM_WEBAPP_AUTH_MAX_AGE")
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
	biginConfiguration, err := loadBiginToolConfiguration(configuration)
	if err != nil {
		return nil, err
	}
	gmailConfiguration, err := loadGmailToolConfiguration(configuration)
	if err != nil {
		return nil, err
	}
	mcpServers, err := loadMCPServerConfigurations(configuration)
	if err != nil {
		return nil, err
	}

	openAIModel := configuration.GetString("openai.model")
	llmProvider := configuration.GetString("llm.provider")
	llmTariffsDirectory := configuration.GetString("llm.tariffs_dir")
	llmPricing, err := llm.LoadPricing(llmTariffsDirectory, llmProvider, openAIModel)
	if err != nil {
		return nil, err
	}

	return &Config{
		Environment:              strings.TrimSpace(configuration.GetString("env")),
		TelegramBotToken:         telegramBotToken,
		TelegramGroupChatID:      telegramGroupChatID,
		DatabaseURL:              databaseURL,
		OpenAIAPIKey:             openAIAPIKey,
		OpenAIModel:              openAIModel,
		OpenAITranscriptionModel: configuration.GetString("openai.transcription_model"),
		OpenAIBaseURL:            configuration.GetString("openai.base_url"),
		LLMProvider:              llmProvider,
		LLMTariffsDirectory:      llmTariffsDirectory,
		LLMMaxTokens:             llmMaxTokens,
		LLMPricing:               llmPricing,
		LLMHistoryMaxMessages:    llmHistoryMaxMessages,
		ToolCallMaxIterations:    toolCallMaxIterations,
		Tools: ToolsConfig{
			CurrentTime: CurrentTimeToolConfig{
				Enabled:         currentTimeEnabled,
				DefaultTimezone: currentTimeDefaultTimezone,
			},
			Bigin:      biginConfiguration,
			Gmail:      gmailConfiguration,
			MCPServers: mcpServers,
		},
		AccessPIN:                accessPIN,
		LogLevel:                 configuration.GetString("log_level"),
		Port:                     port,
		AppBaseURL:               appBaseURL,
		TelegramWebAppAuthMaxAge: telegramWebAppAuthMaxAge,
	}, nil
}

// privateTelegramGroupChatID validates and parses the numeric identifier of a
// private Telegram supergroup or forum.
func privateTelegramGroupChatID(rawChatID string) (int64, error) {
	chatIDText := strings.TrimSpace(rawChatID)
	if !strings.HasPrefix(chatIDText, "-100") || len(chatIDText) == len("-100") {
		return 0, fmt.Errorf("TELEGRAM_GROUP_CHAT_ID must start with -100")
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(chatIDText, "-100"), 10, 63); err != nil {
		return 0, fmt.Errorf("TELEGRAM_GROUP_CHAT_ID must contain only digits after -100, got: %s", rawChatID)
	}
	chatID, err := strconv.ParseInt(chatIDText, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("TELEGRAM_GROUP_CHAT_ID is outside the supported range: %s", rawChatID)
	}
	return chatID, nil
}

// loadGmailToolConfiguration enables Gmail only for a complete credential set
// and validates optional OAuth, API, and timeout overrides.
func loadGmailToolConfiguration(configuration *viper.Viper) (GmailToolConfig, error) {
	gmailConfiguration := GmailToolConfig{
		RefreshToken: strings.TrimSpace(configuration.GetString("tools.gmail.refresh_token")),
		ClientID:     strings.TrimSpace(configuration.GetString("tools.gmail.client_id")),
		ClientSecret: strings.TrimSpace(configuration.GetString("tools.gmail.client_secret")),
		OAuthURL:     strings.TrimRight(strings.TrimSpace(configuration.GetString("tools.gmail.oauth_url")), "/"),
		APIURL:       strings.TrimRight(strings.TrimSpace(configuration.GetString("tools.gmail.api_url")), "/"),
	}

	configuredCredentialCount := 0
	for _, credential := range []string{gmailConfiguration.RefreshToken, gmailConfiguration.ClientID, gmailConfiguration.ClientSecret} {
		if credential != "" {
			configuredCredentialCount++
		}
	}
	if configuredCredentialCount == 0 {
		return gmailConfiguration, nil
	}
	if configuredCredentialCount != 3 {
		return GmailToolConfig{}, fmt.Errorf("TOOLS_GMAIL_REFRESH_TOKEN, TOOLS_GMAIL_CLIENT_ID, and TOOLS_GMAIL_CLIENT_SECRET must either all be configured or all be empty")
	}

	var err error
	gmailConfiguration.OAuthURL, err = optionalHTTPURL(gmailConfiguration.OAuthURL, "TOOLS_GMAIL_OAUTH_URL")
	if err != nil {
		return GmailToolConfig{}, err
	}
	gmailConfiguration.APIURL, err = optionalHTTPURL(gmailConfiguration.APIURL, "TOOLS_GMAIL_API_URL")
	if err != nil {
		return GmailToolConfig{}, err
	}
	gmailConfiguration.CallTimeout, err = positiveDuration(configuration.GetString("tools.gmail.timeout"), "TOOLS_GMAIL_TIMEOUT")
	if err != nil {
		return GmailToolConfig{}, err
	}
	gmailConfiguration.Enabled = true
	return gmailConfiguration, nil
}

// loadBiginToolConfiguration enables Bigin only for a complete credential set
// and validates its optional endpoint and timeout overrides.
func loadBiginToolConfiguration(configuration *viper.Viper) (BiginToolConfig, error) {
	biginConfiguration := BiginToolConfig{
		RefreshToken: strings.TrimSpace(configuration.GetString("tools.bigin.refresh_token")),
		ClientID:     strings.TrimSpace(configuration.GetString("tools.bigin.client_id")),
		ClientSecret: strings.TrimSpace(configuration.GetString("tools.bigin.client_secret")),
		AccountsURL:  strings.TrimRight(strings.TrimSpace(configuration.GetString("tools.bigin.accounts_url")), "/"),
		APIURL:       strings.TrimRight(strings.TrimSpace(configuration.GetString("tools.bigin.api_url")), "/"),
	}

	configuredCredentialCount := 0
	for _, credential := range []string{biginConfiguration.RefreshToken, biginConfiguration.ClientID, biginConfiguration.ClientSecret} {
		if credential != "" {
			configuredCredentialCount++
		}
	}
	if configuredCredentialCount == 0 {
		return biginConfiguration, nil
	}
	if configuredCredentialCount != 3 {
		return BiginToolConfig{}, fmt.Errorf("TOOLS_BIGIN_REFRESH_TOKEN, TOOLS_BIGIN_CLIENT_ID, and TOOLS_BIGIN_CLIENT_SECRET must either all be configured or all be empty")
	}

	var err error
	biginConfiguration.AccountsURL, err = optionalHTTPURL(biginConfiguration.AccountsURL, "TOOLS_BIGIN_ACCOUNTS_URL")
	if err != nil {
		return BiginToolConfig{}, err
	}
	biginConfiguration.APIURL, err = optionalHTTPURL(biginConfiguration.APIURL, "TOOLS_BIGIN_API_URL")
	if err != nil {
		return BiginToolConfig{}, err
	}
	biginConfiguration.CallTimeout, err = positiveDuration(configuration.GetString("tools.bigin.timeout"), "TOOLS_BIGIN_TIMEOUT")
	if err != nil {
		return BiginToolConfig{}, err
	}
	biginConfiguration.Enabled = true
	return biginConfiguration, nil
}

// loadMCPServerConfigurations loads the dynamic TOOLS_MCPS list and applies
// each server's optional TOOLS_<NAME>_ENABLED override. Known servers are also
// considered when absent from the list so an explicit true value can enable
// them.
func loadMCPServerConfigurations(configuration *viper.Viper) ([]MCPServerConfig, error) {
	listedServerNames, err := parseMCPServerNames(configuration.GetString("tools.mcps"))
	if err != nil {
		return nil, err
	}

	listedServers := make(map[string]bool, len(listedServerNames))
	candidateServerNames := make([]string, 0, len(listedServerNames)+len(supportedMCPServerNames))
	seenCandidates := make(map[string]bool, len(listedServerNames)+len(supportedMCPServerNames))
	for _, serverName := range listedServerNames {
		listedServers[serverName] = true
		candidateServerNames = append(candidateServerNames, serverName)
		seenCandidates[serverName] = true
	}
	for _, serverName := range supportedMCPServerNames {
		if !seenCandidates[serverName] {
			candidateServerNames = append(candidateServerNames, serverName)
		}
	}

	serverConfigurations := make([]MCPServerConfig, 0, len(candidateServerNames))
	for _, serverName := range candidateServerNames {
		configurationPrefix := "tools." + serverName
		enabled := listedServers[serverName]
		enabledKey := configurationPrefix + ".enabled"
		if configuration.IsSet(enabledKey) {
			environmentVariableName := "TOOLS_" + strings.ToUpper(serverName) + "_ENABLED"
			enabled, err = booleanValue(configuration, enabledKey, environmentVariableName)
			if err != nil {
				return nil, err
			}
		}

		serverConfiguration := MCPServerConfig{
			Name:        serverName,
			Enabled:     enabled,
			URL:         strings.TrimSpace(configuration.GetString(configurationPrefix + ".url")),
			AuthType:    strings.ToLower(strings.TrimSpace(configuration.GetString(configurationPrefix + ".auth_type"))),
			Token:       strings.TrimSpace(configuration.GetString(configurationPrefix + ".token")),
			CallTimeout: defaultMCPTimeout,
		}
		if serverConfiguration.AuthType == "" {
			serverConfiguration.AuthType = "none"
		}
		if configuredTimeout := strings.TrimSpace(configuration.GetString(configurationPrefix + ".timeout")); configuredTimeout != "" {
			serverConfiguration.CallTimeout, err = time.ParseDuration(configuredTimeout)
			if err != nil || serverConfiguration.CallTimeout <= 0 {
				return nil, fmt.Errorf("TOOLS_%s_TIMEOUT must be a positive duration, got: %s", strings.ToUpper(serverName), configuredTimeout)
			}
		}
		if err := validateMCPServerConfiguration(serverConfiguration); err != nil {
			return nil, err
		}
		serverConfigurations = append(serverConfigurations, serverConfiguration)
	}
	return serverConfigurations, nil
}

// parseMCPServerNames normalizes and de-duplicates the comma-separated MCP
// server list while preserving its configured order.
func parseMCPServerNames(rawServerNames string) ([]string, error) {
	serverNames := make([]string, 0)
	seenServerNames := make(map[string]bool)
	for _, rawServerName := range strings.Split(rawServerNames, ",") {
		serverName := strings.ToLower(strings.TrimSpace(rawServerName))
		if serverName == "" {
			continue
		}
		if !mcpServerNamePattern.MatchString(serverName) {
			return nil, fmt.Errorf("TOOLS_MCPS contains invalid server name %q; use lowercase letters, digits, and underscores", serverName)
		}
		if !seenServerNames[serverName] {
			serverNames = append(serverNames, serverName)
			seenServerNames[serverName] = true
		}
	}
	return serverNames, nil
}

// validateMCPServerConfiguration validates settings that are required only
// when a server is enabled.
func validateMCPServerConfiguration(serverConfiguration MCPServerConfig) error {
	if !serverConfiguration.Enabled {
		return nil
	}
	environmentVariablePrefix := "TOOLS_" + strings.ToUpper(serverConfiguration.Name)
	if serverConfiguration.URL == "" {
		return fmt.Errorf("%s_URL is required when the MCP server is enabled", environmentVariablePrefix)
	}
	parsedURL, err := url.ParseRequestURI(serverConfiguration.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return fmt.Errorf("%s_URL must be an absolute HTTP or HTTPS URL, got: %s", environmentVariablePrefix, serverConfiguration.URL)
	}
	if serverConfiguration.AuthType != "none" && serverConfiguration.AuthType != "bearer" {
		return fmt.Errorf("%s_AUTH_TYPE must be none or bearer, got: %s", environmentVariablePrefix, serverConfiguration.AuthType)
	}
	if serverConfiguration.AuthType == "bearer" && serverConfiguration.Token == "" {
		return fmt.Errorf("%s_TOKEN is required when %s_AUTH_TYPE=bearer", environmentVariablePrefix, environmentVariablePrefix)
	}
	return nil
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

// portNumber parses a TCP port accepted by the HTTP server.
func portNumber(rawPort string) (int, error) {
	parsedPort, err := strconv.Atoi(rawPort)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return 0, fmt.Errorf("PORT must be an integer between 1 and 65535, got: %s", rawPort)
	}
	return parsedPort, nil
}

// positiveDuration parses a positive Go duration from an environment value.
func positiveDuration(rawDuration string, environmentVariableName string) (time.Duration, error) {
	parsedDuration, err := time.ParseDuration(strings.TrimSpace(rawDuration))
	if err != nil || parsedDuration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration, got: %s", environmentVariableName, rawDuration)
	}
	return parsedDuration, nil
}

// optionalHTTPURL validates the externally visible base URL when configured.
func optionalHTTPURL(rawURL string, environmentVariableName string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if baseURL == "" {
		return "", nil
	}
	parsedURL, err := url.ParseRequestURI(baseURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return "", fmt.Errorf("%s must be an absolute HTTP or HTTPS URL, got: %s", environmentVariableName, rawURL)
	}
	return baseURL, nil
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
