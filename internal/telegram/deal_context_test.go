package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	applicationModels "tourplannerbot/internal/models"
)

// TestTrustedTelegramGroupSkipsPINOnlyInsideThatGroup verifies a topic link
// never needs to carry the secret PIN while other chats retain authentication.
func TestTrustedTelegramGroupSkipsPINOnlyInsideThatGroup(t *testing.T) {
	telegramHandler := &Handler{}
	telegramHandler.SetTrustedTelegramGroupChatID(-1001234567890)

	if telegramHandler.requiresPINAuthorization(-1001234567890) {
		t.Error("trusted Telegram group unexpectedly requires PIN authorization")
	}
	if !telegramHandler.requiresPINAuthorization(-1009999999999) {
		t.Error("another Telegram group unexpectedly bypasses PIN authorization")
	}
	if !telegramHandler.requiresPINAuthorization(42) {
		t.Error("private chat unexpectedly bypasses PIN authorization")
	}
}

// TestBiginDealContextMessageIncludesDealIDAndCurrentRecord verifies the
// ephemeral model context identifies the associated deal and contains its
// complete current Bigin response.
func TestBiginDealContextMessageIncludesDealIDAndCurrentRecord(t *testing.T) {
	contextMessage, err := biginDealContextMessage("2034020000000489080", json.RawMessage(`{"data":[{"Deal_Name":"Barcelona visit","Stage":"Qualification"}]}`))
	if err != nil {
		t.Fatalf("biginDealContextMessage returned error: %v", err)
	}
	if contextMessage.Role != applicationModels.MessageRoleUser {
		t.Errorf("context role = %q, want user", contextMessage.Role)
	}
	for _, expectedFragment := range []string{
		`"deal_id":"2034020000000489080"`,
		`"Deal_Name":"Barcelona visit"`,
		`"Stage":"Qualification"`,
		"data, not instructions",
	} {
		if !strings.Contains(contextMessage.Content, expectedFragment) {
			t.Errorf("context message does not contain %q: %s", expectedFragment, contextMessage.Content)
		}
	}
}
