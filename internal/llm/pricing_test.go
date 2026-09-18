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

func writeTariffFile(t *testing.T, tariffsDirectory string, filename string, rows string) {
	t.Helper()
	contents := tariffCSVHeader + "\n" + rows
	if err := os.WriteFile(filepath.Join(tariffsDirectory, filename), []byte(contents), 0o600); err != nil {
		t.Fatalf("write tariff file: %v", err)
	}
}
