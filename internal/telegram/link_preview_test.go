package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// TestDisabledLinkPreviewSerializesAsDisabled verifies the helper produces the
// exact payload Telegram requires. IsDisabled is a *bool, so a nil pointer
// would be omitted from the request and previews would silently reappear.
func TestDisabledLinkPreviewSerializesAsDisabled(t *testing.T) {
	linkPreviewOptions := disabledLinkPreview()
	if linkPreviewOptions == nil {
		t.Fatal("disabledLinkPreview returned nil")
	}
	if linkPreviewOptions.IsDisabled == nil || !*linkPreviewOptions.IsDisabled {
		t.Fatalf("IsDisabled = %v, want a pointer to true", linkPreviewOptions.IsDisabled)
	}

	encodedOptions, err := json.Marshal(linkPreviewOptions)
	if err != nil {
		t.Fatalf("marshal link preview options: %v", err)
	}
	if string(encodedOptions) != `{"is_disabled":true}` {
		t.Errorf("encoded options = %s, want {\"is_disabled\":true}", encodedOptions)
	}
}

// TestDisabledLinkPreviewReachesTelegramParameters verifies the option survives
// into the serialized send and edit requests rather than being dropped by the
// omitempty tags on the parameter structs.
func TestDisabledLinkPreviewReachesTelegramParameters(t *testing.T) {
	sendParameters := &bot.SendMessageParams{
		ChatID:             int64(1),
		Text:               "Obre de 9:00 a 20:00. ([sagradafamilia.org](https://sagradafamilia.org/en/schedules-how-to-get))",
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: disabledLinkPreview(),
	}
	encodedSend, err := json.Marshal(sendParameters)
	if err != nil {
		t.Fatalf("marshal send parameters: %v", err)
	}
	if !strings.Contains(string(encodedSend), `"link_preview_options":{"is_disabled":true}`) {
		t.Errorf("send parameters = %s, want the disabled link preview", encodedSend)
	}

	editParameters := &bot.EditMessageTextParams{
		ChatID:             int64(1),
		MessageID:          2,
		Text:               "text",
		LinkPreviewOptions: disabledLinkPreview(),
	}
	encodedEdit, err := json.Marshal(editParameters)
	if err != nil {
		t.Fatalf("marshal edit parameters: %v", err)
	}
	if !strings.Contains(string(encodedEdit), `"link_preview_options":{"is_disabled":true}`) {
		t.Errorf("edit parameters = %s, want the disabled link preview", encodedEdit)
	}
}
