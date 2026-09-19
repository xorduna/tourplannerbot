package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPricingUsesNewestMatchingSnapshot(t *testing.T) {
	tariffsDirectory := t.TempDir()
	writeTariffFile(t, tariffsDirectory, "2026-08-01-openai.csv", "openai,gpt-4o-mini,100,10,1000,https://example.com/old\n")
	writeTariffFile(t, tariffsDirectory, "2026-09-01-openai.csv", "openai,gpt-4o-mini,200,20,2000,https://example.com/new\n")

	pricing, err := LoadPricing(tariffsDirectory, "openai", "gpt-4o-mini")
	if err != nil {
		t.Fatalf("LoadPricing returned an error: %v", err)
	}
	if pricing.InputMicroUSDPerMillion != 200 || pricing.CachedInputMicroUSDPerMillion != 20 || pricing.OutputMicroUSDPerMillion != 2_000 || pricing.Version != "2026-09-01-openai" {
		t.Errorf("pricing = %#v", pricing)
	}
}

func TestLoadPricingFallsBackWhenNewestSnapshotDoesNotContainModel(t *testing.T) {
	tariffsDirectory := t.TempDir()
	writeTariffFile(t, tariffsDirectory, "2026-08-01-openai.csv", "openai,gpt-4o-mini,100,10,1000,https://example.com/old\n")
	writeTariffFile(t, tariffsDirectory, "2026-09-01-openai.csv", "openai,gpt-4.1,200,20,2000,https://example.com/new\n")

	pricing, err := LoadPricing(tariffsDirectory, "openai", "gpt-4o-mini")
	if err != nil {
		t.Fatalf("LoadPricing returned an error: %v", err)
	}
	if pricing.Version != "2026-08-01-openai" {
		t.Errorf("pricing version = %q, want 2026-08-01-openai", pricing.Version)
	}
}

// TestCurrentOpenAITariffSnapshot verifies every supported model uses the
// reviewed standard short-context prices in the current repository snapshot.
func TestCurrentOpenAITariffSnapshot(t *testing.T) {
	testCases := []struct {
		model                    string
		inputMicroUSDPerMillion  int64
		cachedMicroUSDPerMillion int64
		outputMicroUSDPerMillion int64
	}{
		{model: "gpt-4o-mini", inputMicroUSDPerMillion: 150_000, cachedMicroUSDPerMillion: 75_000, outputMicroUSDPerMillion: 600_000},
		{model: "gpt-4o", inputMicroUSDPerMillion: 2_500_000, cachedMicroUSDPerMillion: 1_250_000, outputMicroUSDPerMillion: 10_000_000},
		{model: "gpt-4.1-nano", inputMicroUSDPerMillion: 100_000, cachedMicroUSDPerMillion: 25_000, outputMicroUSDPerMillion: 400_000},
		{model: "gpt-4.1-mini", inputMicroUSDPerMillion: 400_000, cachedMicroUSDPerMillion: 100_000, outputMicroUSDPerMillion: 1_600_000},
		{model: "gpt-4.1", inputMicroUSDPerMillion: 2_000_000, cachedMicroUSDPerMillion: 500_000, outputMicroUSDPerMillion: 8_000_000},
		{model: "gpt-5.5", inputMicroUSDPerMillion: 5_000_000, cachedMicroUSDPerMillion: 500_000, outputMicroUSDPerMillion: 30_000_000},
		{model: "gpt-5.6-luna", inputMicroUSDPerMillion: 200_000, cachedMicroUSDPerMillion: 20_000, outputMicroUSDPerMillion: 1_200_000},
		{model: "gpt-5.6-terra", inputMicroUSDPerMillion: 2_000_000, cachedMicroUSDPerMillion: 200_000, outputMicroUSDPerMillion: 12_000_000},
		{model: "gpt-5.6-sol", inputMicroUSDPerMillion: 4_000_000, cachedMicroUSDPerMillion: 400_000, outputMicroUSDPerMillion: 20_000_000},
		{model: "gpt-6-astra", inputMicroUSDPerMillion: 10_000_000, cachedMicroUSDPerMillion: 1_000_000, outputMicroUSDPerMillion: 50_000_000},
	}

	tariffsDirectory := filepath.Join("..", "..", "tariffs")
	for _, testCase := range testCases {
		t.Run(testCase.model, func(t *testing.T) {
			pricing, err := LoadPricing(tariffsDirectory, "openai", testCase.model)
			if err != nil {
				t.Fatalf("LoadPricing returned an error: %v", err)
			}
			if pricing.Version != "2026-09-20-openai" {
				t.Fatalf("pricing version for %q = %q, want current snapshot", testCase.model, pricing.Version)
			}
			if pricing.InputMicroUSDPerMillion != testCase.inputMicroUSDPerMillion ||
				pricing.CachedInputMicroUSDPerMillion != testCase.cachedMicroUSDPerMillion ||
				pricing.OutputMicroUSDPerMillion != testCase.outputMicroUSDPerMillion {
				t.Fatalf("pricing for %q = %#v", testCase.model, pricing)
			}
		})
	}
}

func writeTariffFile(t *testing.T, tariffsDirectory string, filename string, rows string) {
	t.Helper()
	contents := tariffCSVHeader + "\n" + rows
	if err := os.WriteFile(filepath.Join(tariffsDirectory, filename), []byte(contents), 0o600); err != nil {
		t.Fatalf("write tariff file: %v", err)
	}
}
