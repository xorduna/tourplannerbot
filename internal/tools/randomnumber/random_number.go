// Package randomnumber implements a cryptographically secure random-number tool.
package randomnumber

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"

	"tourplannerbot/internal/tools"
)

const (
	toolName               = "random_number"
	maximumFloatSampleSize = int64(1 << 53)
)

// Tool generates secure integer or floating-point random values inside a range.
type Tool struct {
	randomReader io.Reader
}

// New creates a random-number tool backed by the operating system's secure RNG.
func New() *Tool {
	return &Tool{randomReader: cryptorand.Reader}
}

// Definition describes the random_number function to the LLM.
func (randomNumberTool *Tool) Definition() tools.Definition {
	return tools.Definition{
		Name:        toolName,
		Description: "Generate one cryptographically secure random number in a requested range. Integer bounds are inclusive. Float bounds include minimum and exclude maximum.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"integer", "float"},
					"description": "integer returns a whole number; float returns a decimal number.",
				},
				"minimum": map[string]any{
					"type":        "number",
					"description": "Lower bound of the requested range.",
				},
				"maximum": map[string]any{
					"type":        "number",
					"description": "Upper bound of the requested range.",
				},
			},
			"required":             []string{"mode", "minimum", "maximum"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "native",
	}
}

// Execute validates a range and generates a random integer or float.
func (randomNumberTool *Tool) Execute(_ context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		Mode    string      `json:"mode"`
		Minimum json.Number `json:"minimum"`
		Maximum json.Number `json:"maximum"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", toolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", toolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", toolName, err)
	}

	switch strings.ToLower(strings.TrimSpace(arguments.Mode)) {
	case "integer":
		return randomNumberTool.generateInteger(arguments.Minimum, arguments.Maximum)
	case "float":
		return randomNumberTool.generateFloat(arguments.Minimum, arguments.Maximum)
	default:
		return "", fmt.Errorf("mode must be integer or float")
	}
}

// generateInteger returns one value selected uniformly from the inclusive int64 range.
func (randomNumberTool *Tool) generateInteger(rawMinimum, rawMaximum json.Number) (string, error) {
	minimum, err := rawMinimum.Int64()
	if err != nil {
		return "", fmt.Errorf("minimum must be an integer in the int64 range when mode is integer")
	}
	maximum, err := rawMaximum.Int64()
	if err != nil {
		return "", fmt.Errorf("maximum must be an integer in the int64 range when mode is integer")
	}
	if minimum > maximum {
		return "", fmt.Errorf("minimum must not exceed maximum")
	}

	rangeWidth := new(big.Int).Sub(big.NewInt(maximum), big.NewInt(minimum))
	rangeWidth.Add(rangeWidth, big.NewInt(1))
	randomOffset, err := cryptorand.Int(randomNumberTool.randomReader, rangeWidth)
	if err != nil {
		return "", fmt.Errorf("generate secure random integer: %w", err)
	}
	value := randomOffset.Add(randomOffset, big.NewInt(minimum)).Int64()
	return encodeResult("integer", minimum, maximum, value)
}

// generateFloat returns one value selected uniformly from [minimum, maximum).
func (randomNumberTool *Tool) generateFloat(rawMinimum, rawMaximum json.Number) (string, error) {
	minimum, err := rawMinimum.Float64()
	if err != nil || math.IsNaN(minimum) || math.IsInf(minimum, 0) {
		return "", fmt.Errorf("minimum must be a finite number when mode is float")
	}
	maximum, err := rawMaximum.Float64()
	if err != nil || math.IsNaN(maximum) || math.IsInf(maximum, 0) {
		return "", fmt.Errorf("maximum must be a finite number when mode is float")
	}
	if minimum > maximum {
		return "", fmt.Errorf("minimum must not exceed maximum")
	}
	if minimum == maximum {
		return encodeResult("float", minimum, maximum, minimum)
	}

	randomSample, err := cryptorand.Int(randomNumberTool.randomReader, big.NewInt(maximumFloatSampleSize))
	if err != nil {
		return "", fmt.Errorf("generate secure random float: %w", err)
	}
	fraction := float64(randomSample.Int64()) / float64(maximumFloatSampleSize)
	value := minimum*(1-fraction) + maximum*fraction
	if value >= maximum {
		value = math.Nextafter(maximum, minimum)
	}
	return encodeResult("float", minimum, maximum, value)
}

// encodeResult returns one canonical JSON tool result.
func encodeResult(mode string, minimum, maximum, value any) (string, error) {
	encodedResult, err := json.Marshal(struct {
		Mode    string `json:"mode"`
		Minimum any    `json:"minimum"`
		Maximum any    `json:"maximum"`
		Value   any    `json:"value"`
	}{
		Mode:    mode,
		Minimum: minimum,
		Maximum: maximum,
		Value:   value,
	})
	if err != nil {
		return "", fmt.Errorf("encode %s result: %w", toolName, err)
	}
	return string(encodedResult), nil
}
