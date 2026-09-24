package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setRequiredEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-telegram-token")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_TRANSCRIPTION_MODEL", "")
	t.Setenv("ACCESS_PIN", "1234")
	t.Setenv("PORT", "")
	t.Setenv("APP_BASE_URL", "")
	t.Setenv("TELEGRAM_WEBAPP_AUTH_MAX_AGE", "")
	t.Setenv("TOOLS_MCPS", "")
	t.Setenv("TOOLS_CURRENT_TIME_ENABLED", "")
	t.Setenv("TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE", "")
	t.Setenv("TOOLS_BIGIN_REFRESH_TOKEN", "")
	t.Setenv("TOOLS_BIGIN_CLIENT_ID", "")
	t.Setenv("TOOLS_BIGIN_CLIENT_SECRET", "")
	t.Setenv("TOOLS_BIGIN_ACCOUNTS_URL", "")
	t.Setenv("TOOLS_BIGIN_API_URL", "")
	t.Setenv("TOOLS_BIGIN_TIMEOUT", "")
	t.Setenv("TOOLS_GMAIL_REFRESH_TOKEN", "")
	t.Setenv("TOOLS_GMAIL_CLIENT_ID", "")
	t.Setenv("TOOLS_GMAIL_CLIENT_SECRET", "")
	t.Setenv("TOOLS_GMAIL_OAUTH_URL", "")
	t.Setenv("TOOLS_GMAIL_API_URL", "")
	t.Setenv("TOOLS_GMAIL_TIMEOUT", "")
	t.Setenv("TOOLS_WIKIPEDIA_ENABLED", "")
	t.Setenv("TOOLS_WIKIPEDIA_URL", "")
	t.Setenv("TOOLS_WIKIPEDIA_AUTH_TYPE", "")
	t.Setenv("TOOLS_WIKIPEDIA_TOKEN", "")
	t.Setenv("TOOLS_WIKIPEDIA_TIMEOUT", "")
	t.Setenv("TOOLS_OPENSTREETMAP_ENABLED", "")
	t.Setenv("TOOLS_OPENSTREETMAP_URL", "")
	t.Setenv("TOOLS_OPENSTREETMAP_AUTH_TYPE", "")
	t.Setenv("TOOLS_OPENSTREETMAP_TOKEN", "")
	t.Setenv("TOOLS_OPENSTREETMAP_TIMEOUT", "")

	tariffsDirectory := t.TempDir()
	t.Setenv("LLM_TARIFFS_DIR", tariffsDirectory)
	tariffFile := filepath.Join(tariffsDirectory, "2026-09-18-openai.csv")
	tariffCSV := "provider,model,input_micro_usd_per_million,cached_input_micro_usd_per_million,output_micro_usd_per_million,source_url\n" +
		"openai,gpt-5.5,5000000,500000,30000000,https://example.com/gpt-5.5\n" +
		"openai,gpt-4o-mini,150000,75000,600000,https://example.com/gpt-4o-mini\n"
	if err := os.WriteFile(tariffFile, []byte(tariffCSV), 0o600); err != nil {
		t.Fatalf("write tariff CSV: %v", err)
	}
}

// TestLoadFromEnvironmentUsesGPT55ByDefault verifies the default model and its
// pricing are loaded from the CSV snapshot.
func TestLoadFromEnvironmentUsesGPT55ByDefault(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("OPENAI_MODEL", "")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.OpenAIModel != "gpt-5.5" {
		t.Errorf("OpenAIModel = %q, want gpt-5.5", applicationConfig.OpenAIModel)
	}
	if applicationConfig.OpenAITranscriptionModel != "gpt-transcribe" {
		t.Errorf("OpenAITranscriptionModel = %q, want gpt-transcribe", applicationConfig.OpenAITranscriptionModel)
	}
	if applicationConfig.LLMHistoryMaxMessages != 20 {
		t.Errorf("LLMHistoryMaxMessages = %d, want 20", applicationConfig.LLMHistoryMaxMessages)
	}
	if applicationConfig.Port != 8080 {
		t.Errorf("Port = %d, want 8080", applicationConfig.Port)
	}
	if applicationConfig.AppBaseURL != "" {
		t.Errorf("AppBaseURL = %q, want empty", applicationConfig.AppBaseURL)
	}
	if applicationConfig.TelegramWebAppAuthMaxAge != 5*time.Minute {
		t.Errorf("TelegramWebAppAuthMaxAge = %s, want 5m", applicationConfig.TelegramWebAppAuthMaxAge)
	}
	if applicationConfig.LLMProvider != "openai" {
		t.Errorf("LLMProvider = %q, want openai", applicationConfig.LLMProvider)
	}
	if !applicationConfig.Tools.CurrentTime.Enabled {
		t.Error("Tools.CurrentTime.Enabled = false, want true")
	}
	if applicationConfig.Tools.CurrentTime.DefaultTimezone != "Europe/Madrid" {
		t.Errorf("Tools.CurrentTime.DefaultTimezone = %q, want Europe/Madrid", applicationConfig.Tools.CurrentTime.DefaultTimezone)
	}
	if applicationConfig.LLMPricing == nil {
		t.Fatal("LLMPricing = nil, want pricing from the CSV")
	}
	if applicationConfig.LLMPricing.InputMicroUSDPerMillion != 5_000_000 || applicationConfig.LLMPricing.Version != "2026-09-18-openai" {
		t.Errorf("LLMPricing = %#v", applicationConfig.LLMPricing)
	}
}

func TestLoadFromEnvironmentLoadsWebServerSettings(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("PORT", "9090")
	t.Setenv("APP_BASE_URL", "https://example.ngrok.app/")
	t.Setenv("TELEGRAM_WEBAPP_AUTH_MAX_AGE", "10m")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.Port != 9090 {
		t.Errorf("Port = %d, want 9090", applicationConfig.Port)
	}
	if applicationConfig.AppBaseURL != "https://example.ngrok.app" {
		t.Errorf("AppBaseURL = %q, want https://example.ngrok.app", applicationConfig.AppBaseURL)
	}
	if applicationConfig.TelegramWebAppAuthMaxAge != 10*time.Minute {
		t.Errorf("TelegramWebAppAuthMaxAge = %s, want 10m", applicationConfig.TelegramWebAppAuthMaxAge)
	}
}

// TestLoadFromEnvironmentLoadsTranscriptionModelOverride verifies audio transcription is independently configurable.
func TestLoadFromEnvironmentLoadsTranscriptionModelOverride(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("OPENAI_TRANSCRIPTION_MODEL", "gpt-4o-mini-transcribe")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.OpenAITranscriptionModel != "gpt-4o-mini-transcribe" {
		t.Errorf("OpenAITranscriptionModel = %q", applicationConfig.OpenAITranscriptionModel)
	}
}

func TestLoadFromEnvironmentRejectsInvalidWebServerSettings(t *testing.T) {
	testCases := []struct {
		name       string
		port       string
		appBaseURL string
		authMaxAge string
	}{
		{name: "port below range", port: "0"},
		{name: "port above range", port: "65536"},
		{name: "non-numeric port", port: "http"},
		{name: "invalid base URL", appBaseURL: "example.com"},
		{name: "invalid auth age", authMaxAge: "forever"},
		{name: "non-positive auth age", authMaxAge: "0s"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			setRequiredEnvironment(t)
			if testCase.port != "" {
				t.Setenv("PORT", testCase.port)
			}
			if testCase.appBaseURL != "" {
				t.Setenv("APP_BASE_URL", testCase.appBaseURL)
			}
			if testCase.authMaxAge != "" {
				t.Setenv("TELEGRAM_WEBAPP_AUTH_MAX_AGE", testCase.authMaxAge)
			}

			if _, err := LoadFromEnvironment(); err == nil {
				t.Fatal("LoadFromEnvironment returned nil error for invalid web server settings")
			}
		})
	}
}

func TestLoadFromEnvironmentLoadsCurrentTimeOverrides(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_CURRENT_TIME_ENABLED", "false")
	t.Setenv("TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE", "America/New_York")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.Tools.CurrentTime.Enabled {
		t.Error("Tools.CurrentTime.Enabled = true, want false")
	}
	if applicationConfig.Tools.CurrentTime.DefaultTimezone != "America/New_York" {
		t.Errorf("Tools.CurrentTime.DefaultTimezone = %q", applicationConfig.Tools.CurrentTime.DefaultTimezone)
	}
}

// TestLoadFromEnvironmentEnablesBiginForCompleteCredentials verifies the three
// OAuth values are sufficient to register the native integration.
func TestLoadFromEnvironmentEnablesBiginForCompleteCredentials(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_BIGIN_REFRESH_TOKEN", "refresh-token")
	t.Setenv("TOOLS_BIGIN_CLIENT_ID", "client-id")
	t.Setenv("TOOLS_BIGIN_CLIENT_SECRET", "client-secret")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if !applicationConfig.Tools.Bigin.Enabled {
		t.Fatal("Tools.Bigin.Enabled = false, want true")
	}
	if applicationConfig.Tools.Bigin.AccountsURL != "https://accounts.zoho.eu" {
		t.Errorf("Tools.Bigin.AccountsURL = %q", applicationConfig.Tools.Bigin.AccountsURL)
	}
	if applicationConfig.Tools.Bigin.APIURL != "https://www.zohoapis.eu" {
		t.Errorf("Tools.Bigin.APIURL = %q", applicationConfig.Tools.Bigin.APIURL)
	}
	if applicationConfig.Tools.Bigin.CallTimeout != 30*time.Second {
		t.Errorf("Tools.Bigin.CallTimeout = %s, want 30s", applicationConfig.Tools.Bigin.CallTimeout)
	}
}

// TestLoadFromEnvironmentRejectsPartialBiginCredentials prevents a deployment
// from silently starting without a usable OAuth credential set.
func TestLoadFromEnvironmentRejectsPartialBiginCredentials(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_BIGIN_CLIENT_ID", "client-id")

	if _, err := LoadFromEnvironment(); err == nil {
		t.Fatal("LoadFromEnvironment returned nil error for partial Bigin credentials")
	}
}

// TestLoadFromEnvironmentEnablesGmailForCompleteCredentials verifies the three
// OAuth values enable the native Gmail integration with safe defaults.
func TestLoadFromEnvironmentEnablesGmailForCompleteCredentials(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_GMAIL_REFRESH_TOKEN", "refresh-token")
	t.Setenv("TOOLS_GMAIL_CLIENT_ID", "client-id")
	t.Setenv("TOOLS_GMAIL_CLIENT_SECRET", "client-secret")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if !applicationConfig.Tools.Gmail.Enabled {
		t.Fatal("Tools.Gmail.Enabled = false, want true")
	}
	if applicationConfig.Tools.Gmail.OAuthURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("Tools.Gmail.OAuthURL = %q", applicationConfig.Tools.Gmail.OAuthURL)
	}
	if applicationConfig.Tools.Gmail.APIURL != "https://gmail.googleapis.com" {
		t.Errorf("Tools.Gmail.APIURL = %q", applicationConfig.Tools.Gmail.APIURL)
	}
	if applicationConfig.Tools.Gmail.CallTimeout != 30*time.Second {
		t.Errorf("Tools.Gmail.CallTimeout = %s, want 30s", applicationConfig.Tools.Gmail.CallTimeout)
	}
}

// TestLoadFromEnvironmentRejectsPartialGmailCredentials prevents startup with
// an integration that cannot refresh an access token.
func TestLoadFromEnvironmentRejectsPartialGmailCredentials(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_GMAIL_CLIENT_ID", "client-id")

	if _, err := LoadFromEnvironment(); err == nil {
		t.Fatal("LoadFromEnvironment returned nil error for partial Gmail credentials")
	}
}

// TestLoadFromEnvironmentLoadsMCPServers verifies list membership enables MCP
// servers and their connection settings are parsed dynamically.
func TestLoadFromEnvironmentLoadsMCPServers(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_MCPS", "wikipedia, openstreetmap, wikipedia")
	t.Setenv("TOOLS_WIKIPEDIA_URL", "http://127.0.0.1:8088/mcp")
	t.Setenv("TOOLS_WIKIPEDIA_TIMEOUT", "12s")
	t.Setenv("TOOLS_OPENSTREETMAP_URL", "http://127.0.0.1:3010/mcp")
	t.Setenv("TOOLS_OPENSTREETMAP_AUTH_TYPE", "bearer")
	t.Setenv("TOOLS_OPENSTREETMAP_TOKEN", "internal-token")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if len(applicationConfig.Tools.MCPServers) != 2 {
		t.Fatalf("len(Tools.MCPServers) = %d, want 2", len(applicationConfig.Tools.MCPServers))
	}
	wikipediaConfiguration := applicationConfig.Tools.MCPServers[0]
	if wikipediaConfiguration.Name != "wikipedia" || !wikipediaConfiguration.Enabled || wikipediaConfiguration.URL != "http://127.0.0.1:8088/mcp" || wikipediaConfiguration.CallTimeout != 12*time.Second {
		t.Errorf("Wikipedia configuration = %#v", wikipediaConfiguration)
	}
	openStreetMapConfiguration := applicationConfig.Tools.MCPServers[1]
	if openStreetMapConfiguration.Name != "openstreetmap" || !openStreetMapConfiguration.Enabled || openStreetMapConfiguration.AuthType != "bearer" || openStreetMapConfiguration.Token != "internal-token" {
		t.Errorf("OpenStreetMap configuration = %#v", openStreetMapConfiguration)
	}
}

// TestLoadFromEnvironmentAppliesMCPEnabledOverrides verifies an explicit false
// disables a listed server and an explicit true enables a known unlisted one.
func TestLoadFromEnvironmentAppliesMCPEnabledOverrides(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_MCPS", "wikipedia")
	t.Setenv("TOOLS_WIKIPEDIA_ENABLED", "false")
	t.Setenv("TOOLS_OPENSTREETMAP_ENABLED", "true")
	t.Setenv("TOOLS_OPENSTREETMAP_URL", "http://127.0.0.1:3010/mcp")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.Tools.MCPServers[0].Enabled {
		t.Error("listed Wikipedia server remained enabled after explicit false override")
	}
	if !applicationConfig.Tools.MCPServers[1].Enabled {
		t.Error("unlisted OpenStreetMap server remained disabled after explicit true override")
	}
}

// TestLoadFromEnvironmentRejectsEnabledMCPWithoutURL verifies enabled servers
// cannot reach startup with an incomplete endpoint configuration.
func TestLoadFromEnvironmentRejectsEnabledMCPWithoutURL(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_MCPS", "wikipedia")

	if _, err := LoadFromEnvironment(); err == nil {
		t.Fatal("LoadFromEnvironment returned nil error for an enabled MCP server without a URL")
	}
}

// TestLoadFromEnvironmentRequiresBearerToken verifies bearer authentication is
// rejected when no token is configured.
func TestLoadFromEnvironmentRequiresBearerToken(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_MCPS", "wikipedia")
	t.Setenv("TOOLS_WIKIPEDIA_URL", "http://127.0.0.1:8088/mcp")
	t.Setenv("TOOLS_WIKIPEDIA_AUTH_TYPE", "bearer")

	if _, err := LoadFromEnvironment(); err == nil {
		t.Fatal("LoadFromEnvironment returned nil error for bearer authentication without a token")
	}
}

func TestLoadFromEnvironmentRejectsInvalidCurrentTimeTimezone(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE", "Mars/Olympus")

	if _, err := LoadFromEnvironment(); err == nil {
		t.Fatal("LoadFromEnvironment returned nil error for an invalid timezone")
	}
}

func TestLoadFromEnvironmentLoadsConfiguredModelPricing(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("OPENAI_MODEL", "gpt-4o-mini")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.LLMPricing == nil {
		t.Fatal("LLMPricing = nil, want configured model pricing")
	}
	if applicationConfig.LLMPricing.InputMicroUSDPerMillion != 150_000 || applicationConfig.LLMPricing.CachedInputMicroUSDPerMillion != 75_000 || applicationConfig.LLMPricing.OutputMicroUSDPerMillion != 600_000 {
		t.Errorf("LLMPricing = %#v", applicationConfig.LLMPricing)
	}
}
