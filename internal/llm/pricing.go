package llm

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const tariffCSVHeader = "provider,model,input_micro_usd_per_million,cached_input_micro_usd_per_million,output_micro_usd_per_million,source_url"

// LoadPricing finds the newest CSV snapshot that contains an exact provider
// and model match. CSV filenames start with a YYYY-MM-DD date so lexical order
// is also chronological order.
func LoadPricing(tariffsDirectory string, provider string, model string) (*Pricing, error) {
	entries, err := os.ReadDir(tariffsDirectory)
	if err != nil {
		return nil, fmt.Errorf("read tariffs directory %q: %w", tariffsDirectory, err)
	}

	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".csv") {
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(filenames)))

	for _, filename := range filenames {
		pricing, found, err := loadPricingFromCSV(filepath.Join(tariffsDirectory, filename), provider, model)
		if err != nil {
			return nil, err
		}
		if found {
			pricing.Version = strings.TrimSuffix(filename, ".csv")
			return pricing, nil
		}
	}

	return nil, fmt.Errorf("no tariff found for provider %q and model %q in %q", provider, model, tariffsDirectory)
}

func loadPricingFromCSV(filename string, provider string, model string) (*Pricing, bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, false, fmt.Errorf("open tariff file %q: %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, false, fmt.Errorf("read tariff header from %q: %w", filename, err)
	}
	if strings.Join(header, ",") != tariffCSVHeader {
		return nil, false, fmt.Errorf("tariff file %q has an unexpected header", filename)
	}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("read tariff file %q: %w", filename, err)
		}
		if len(record) != 6 {
			return nil, false, fmt.Errorf("tariff file %q has a row with %d columns, want 6", filename, len(record))
		}
		if record[0] != provider || record[1] != model {
			continue
		}

		inputPrice, err := parseTariffPrice(filename, "input", record[2])
		if err != nil {
			return nil, false, err
		}
		cachedInputPrice, err := parseTariffPrice(filename, "cached input", record[3])
		if err != nil {
			return nil, false, err
		}
		outputPrice, err := parseTariffPrice(filename, "output", record[4])
		if err != nil {
			return nil, false, err
		}
		return &Pricing{
			InputMicroUSDPerMillion:       inputPrice,
			CachedInputMicroUSDPerMillion: cachedInputPrice,
			OutputMicroUSDPerMillion:      outputPrice,
		}, true, nil
	}
}

func parseTariffPrice(filename string, priceName string, rawValue string) (int64, error) {
	price, err := strconv.ParseInt(rawValue, 10, 64)
	if err != nil || price < 0 {
		return 0, fmt.Errorf("tariff file %q has an invalid %s price %q", filename, priceName, rawValue)
	}
	return price, nil
}
