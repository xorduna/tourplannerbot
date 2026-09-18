// Package prompt loads the versioned system prompts used by the bot.
package prompt

import (
	"fmt"
	"os"
	"strings"
)

// Load reads a non-empty prompt file once during application startup.
func Load(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", path, err)
	}
	prompt := strings.TrimSpace(string(contents))
	if prompt == "" {
		return "", fmt.Errorf("prompt file %q is empty", path)
	}
	return prompt, nil
}
