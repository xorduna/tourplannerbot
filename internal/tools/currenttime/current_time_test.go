package currenttime

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestExecuteUsesDefaultTimezone verifies that omitted arguments use the
// configured timezone and return structured JSON.
func TestExecuteUsesDefaultTimezone(t *testing.T) {
	currentTimeTool, err := New("Europe/Madrid")
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	currentTimeTool.now = func() time.Time {
		return time.Date(2026, time.January, 15, 12, 30, 0, 0, time.UTC)
	}

	encodedResult, err := currentTimeTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	var result struct {
		Timezone      string `json:"timezone"`
		LocalTime     string `json:"local_time"`
		UTCOffset     string `json:"utc_offset"`
		UnixTimestamp int64  `json:"unix_timestamp"`
	}
	if err := json.Unmarshal([]byte(encodedResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Timezone != "Europe/Madrid" || result.LocalTime != "2026-01-15T13:30:00+01:00" || result.UTCOffset != "+01:00" {
		t.Errorf("result = %#v", result)
	}
	if result.UnixTimestamp != 1768480200 {
		t.Errorf("UnixTimestamp = %d, want 1768480200", result.UnixTimestamp)
	}
}

// TestExecuteAcceptsTimezoneOverride verifies that a valid IANA timezone can be
// selected by the model for one call.
func TestExecuteAcceptsTimezoneOverride(t *testing.T) {
	currentTimeTool, err := New("Europe/Madrid")
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	currentTimeTool.now = func() time.Time {
		return time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	}

	encodedResult, err := currentTimeTool.Execute(context.Background(), json.RawMessage(`{"timezone":"America/New_York"}`))
	if err != nil {
		t.Fatalf("Execute returned an error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(encodedResult), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["local_time"] != "2026-07-01T08:00:00-04:00" {
		t.Errorf("local_time = %v", result["local_time"])
	}
}

// TestExecuteRejectsInvalidTimezone verifies that invalid IANA names are
// returned as tool errors rather than silently falling back.
func TestExecuteRejectsInvalidTimezone(t *testing.T) {
	currentTimeTool, err := New("Europe/Madrid")
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if _, err := currentTimeTool.Execute(context.Background(), json.RawMessage(`{"timezone":"Mars/Olympus"}`)); err == nil {
		t.Fatal("Execute returned nil error for an invalid timezone")
	}
}
