package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

const testDraftID = "2ee30369-f4ae-4741-8cfc-e58f2eb9b5f1"

func TestDraftPreviewReplyMarkupUsesInlineWebAppInPrivateChat(t *testing.T) {
	handler := &Handler{appBaseURL: "https://example.test", botUsername: "tourplannerbot"}
	markup := handler.draftPreviewReplyMarkup(models.ChatTypePrivate, testDraftID)
	button := onlyInlineButton(t, markup)

	if button.WebApp == nil || button.WebApp.URL != "https://example.test/miniapp?draft="+testDraftID {
		t.Fatalf("private button WebApp = %#v, want direct Mini App URL", button.WebApp)
	}
	if button.URL != "" {
		t.Errorf("private button URL = %q, want empty", button.URL)
	}
}

func TestDraftPreviewReplyMarkupUsesStartAppLinkInTopic(t *testing.T) {
	handler := &Handler{}
	handler.SetBotUsername("@tourplannerbot")
	markup := handler.draftPreviewReplyMarkup(models.ChatTypeSupergroup, testDraftID)
	button := onlyInlineButton(t, markup)

	wantURL := "https://t.me/tourplannerbot?startapp=draft_" + testDraftID
	if button.URL != wantURL {
		t.Fatalf("topic button URL = %q, want %q", button.URL, wantURL)
	}
	if button.WebApp != nil {
		t.Errorf("topic button WebApp = %#v, want nil", button.WebApp)
	}
}

func TestDraftPreviewReplyMarkupNeedsBotUsernameForTopic(t *testing.T) {
	if markup := (&Handler{}).draftPreviewReplyMarkup(models.ChatTypeSupergroup, testDraftID); markup != nil {
		t.Fatalf("topic markup = %#v without bot username, want nil", markup)
	}
}

func onlyInlineButton(t *testing.T, replyMarkup models.ReplyMarkup) models.InlineKeyboardButton {
	t.Helper()
	markup, ok := replyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("reply markup = %#v, want one inline button", replyMarkup)
	}
	return markup.InlineKeyboard[0][0]
}
