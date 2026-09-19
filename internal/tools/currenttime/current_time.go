// Package currenttime implements the native tool that returns the current time
// in an IANA timezone.
package currenttime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"tourplannerbot/internal/tools"
)

const toolName = "current_time"

// Tool returns the current time using a configured default timezone.
type Tool struct {
	defaultTimezone string
	now             func() time.Time
}

// New creates a current-time tool and validates its default IANA timezone.
func New(defaultTimezone string) (*Tool, error) {
	if _, err := time.LoadLocation(defaultTimezone); err != nil {
		return nil, fmt.Errorf("load default timezone %q: %w", defaultTimezone, err)
	}
	return &Tool{defaultTimezone: defaultTimezone, now: time.Now}, nil
}

// Definition describes the current_time function to the LLM.
func (currentTimeTool *Tool) Definition() tools.Definition {
	return tools.Definition{
		Name:        toolName,
		Description: fmt.Sprintf("Return the current date and time in an IANA timezone. Use %s when no timezone is supplied.", currentTimeTool.defaultTimezone),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone": map[string]any{
					"type":        "string",
					"description": "Optional IANA timezone such as Europe/Madrid or America/New_York.",
				},
			},
			"additionalProperties": false,
		},
		Strict: false,
	}
}

// Execute returns a JSON object containing the current local time, UTC offset,
// timezone, and Unix timestamp.
func (currentTimeTool *Tool) Execute(_ context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		Timezone string `json:"timezone"`
	}{}
	if len(bytes.TrimSpace(rawArguments)) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(rawArguments))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&arguments); err != nil {
			return "", fmt.Errorf("decode current_time arguments: %w", err)
		}
		if err := ensureJSONDocumentEnded(decoder); err != nil {
			return "", err
		}
	}

	timezone := strings.TrimSpace(arguments.Timezone)
	if timezone == "" {
		timezone = currentTimeTool.defaultTimezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", fmt.Errorf("timezone %q is not a valid IANA timezone: %w", timezone, err)
	}

	currentTime := currentTimeTool.now().In(location)
	_, utcOffsetSeconds := currentTime.Zone()
	result := struct {
		Timezone      string `json:"timezone"`
		LocalTime     string `json:"local_time"`
		UTCOffset     string `json:"utc_offset"`
		UnixTimestamp int64  `json:"unix_timestamp"`
	}{
		Timezone:      timezone,
		LocalTime:     currentTime.Format(time.RFC3339),
		UTCOffset:     formatUTCOffset(utcOffsetSeconds),
		UnixTimestamp: currentTime.Unix(),
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode current_time result: %w", err)
	}
	return string(encodedResult), nil
}

// ensureJSONDocumentEnded rejects trailing JSON values after the arguments object.
func ensureJSONDocumentEnded(decoder *json.Decoder) error {
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode current_time arguments: multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode current_time arguments: %w", err)
	}
	return nil
}

// formatUTCOffset formats an offset in seconds as +HH:MM or -HH:MM.
func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offsetSeconds/3600, (offsetSeconds%3600)/60)
}
