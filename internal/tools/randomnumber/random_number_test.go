package randomnumber

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestIntegerResultStaysInsideInclusiveRange verifies both integer bounds are valid results.
func TestIntegerResultStaysInsideInclusiveRange(t *testing.T) {
	randomNumberTool := New()
	for attemptNumber := 0; attemptNumber < 20; attemptNumber++ {
		result, err := randomNumberTool.Execute(context.Background(), json.RawMessage(`{"mode":"integer","minimum":-2,"maximum":2}`))
		if err != nil {
			t.Fatalf("Execute returned an error: %v", err)
		}
		var decodedResult struct {
			Mode  string `json:"mode"`
			Value int64  `json:"value"`
		}
		if err := json.Unmarshal([]byte(result), &decodedResult); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if decodedResult.Mode != "integer" || decodedResult.Value < -2 || decodedResult.Value > 2 {
			t.Errorf("result = %s", result)
		}
	}
}

// TestFloatResultStaysInsideHalfOpenRange verifies float values stay within [minimum, maximum).
func TestFloatResultStaysInsideHalfOpenRange(t *testing.T) {
	randomNumberTool := New()
	for attemptNumber := 0; attemptNumber < 20; attemptNumber++ {
		result, err := randomNumberTool.Execute(context.Background(), json.RawMessage(`{"mode":"float","minimum":1.25,"maximum":4.5}`))
		if err != nil {
			t.Fatalf("Execute returned an error: %v", err)
		}
		var decodedResult struct {
			Mode  string  `json:"mode"`
			Value float64 `json:"value"`
		}
		if err := json.Unmarshal([]byte(result), &decodedResult); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if decodedResult.Mode != "float" || decodedResult.Value < 1.25 || decodedResult.Value >= 4.5 {
			t.Errorf("result = %s", result)
		}
	}
}

// TestExecuteRejectsInvalidRangesAndModes verifies malformed requests never produce a result.
func TestExecuteRejectsInvalidRangesAndModes(t *testing.T) {
	randomNumberTool := New()
	for _, rawArguments := range []string{
		`{"mode":"integer","minimum":1.5,"maximum":2}`,
		`{"mode":"float","minimum":3,"maximum":2}`,
		`{"mode":"decimal","minimum":1,"maximum":2}`,
		`{"mode":"integer","minimum":1,"maximum":2,"unexpected":true}`,
		`{"mode":"integer","minimum":1,"maximum":2} {}`,
	} {
		if _, err := randomNumberTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
}

// TestDefinitionIsStrict verifies the model receives a closed schema with both modes.
func TestDefinitionIsStrict(t *testing.T) {
	definition := New().Definition()
	if definition.Name != "random_number" || definition.Source != "native" || !definition.Strict {
		t.Errorf("definition = %#v", definition)
	}
	if !strings.Contains(definition.Description, "Integer") {
		t.Errorf("description = %q", definition.Description)
	}
}
