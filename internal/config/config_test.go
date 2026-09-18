package config

import (
	"os"
	"path/filepath"
	"testing"
)

func setRequiredEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-telegram-token")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ACCESS_PIN", "1234")

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
	if applicationConfig.LLMHistoryMaxMessages != 20 {
		t.Errorf("LLMHistoryMaxMessages = %d, want 20", applicationConfig.LLMHistoryMaxMessages)
	}
	if applicationConfig.LLMProvider != "openai" {
		t.Errorf("LLMProvider = %q, want openai", applicationConfig.LLMProvider)
	}
	if applicationConfig.LLMPricing == nil {
		t.Fatal("LLMPricing = nil, want pricing from the CSV")
	}
	if applicationConfig.LLMPricing.InputMicroUSDPerMillion != 5_000_000 || applicationConfig.LLMPricing.Version != "2026-09-18-openai" {
		t.Errorf("LLMPricing = %#v", applicationConfig.LLMPricing)
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
