package config

import "testing"

// TestLoadFromEnvironmentUsesGPT55ByDefault verifies the Slice 3 model default.
func TestLoadFromEnvironmentUsesGPT55ByDefault(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-telegram-token")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ACCESS_PIN", "1234")
	t.Setenv("OPENAI_MODEL", "")

	applicationConfig, err := LoadFromEnvironment()
	if err != nil {
		t.Fatalf("LoadFromEnvironment returned an error: %v", err)
	}
	if applicationConfig.OpenAIModel != "gpt-5.5" {
		t.Errorf("OpenAIModel = %q, want gpt-5.5", applicationConfig.OpenAIModel)
	}
}
