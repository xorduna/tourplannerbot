package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"tourplannerbot/internal/models"

	"github.com/go-telegram/bot"
)

func TestDraftPreviewUpdaterReplacesSavedTelegramPreview(t *testing.T) {
	recordingHTTPClient := &recordingTelegramHTTPClient{}
	telegramBot, err := bot.New("test-token", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, recordingHTTPClient))
	if err != nil {
		t.Fatalf("create Telegram bot: %v", err)
	}
	contentJSON, err := models.NewTiptapDocumentFromMarkdown("Hola **Diana**")
	if err != nil {
		t.Fatalf("create draft content: %v", err)
	}
	telegramMessageID := int64(77)
	draft := &models.Draft{
		ChatID:            123,
		TelegramMessageID: &telegramMessageID,
		ContentJSON:       contentJSON,
		BodyText:          "Hola Diana",
	}

	handler := &Handler{appBaseURL: "https://example.test", botUsername: "tourplannerbot"}
	if err := NewDraftPreviewUpdater(telegramBot, handler).UpdateDraftPreview(context.Background(), draft); err != nil {
		t.Fatalf("UpdateDraftPreview returned error: %v", err)
	}

	requests := recordingHTTPClient.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("recorded %d Telegram requests, want 2: %#v", len(requests), requests)
	}
	if requests[0].methodName != "getChat" || requests[0].fields["chat_id"] != "123" {
		t.Errorf("first Telegram request = %#v, want chat lookup", requests[0])
	}
	request := requests[1]
	if request.methodName != "editMessageText" || request.fields["chat_id"] != "123" || request.fields["message_id"] != "77" {
		t.Errorf("Telegram request = %#v, want edit of chat 123 message 77", request)
	}
	wantText := "He actualitzat aquesta proposta.\n\n────────\n\nHola <b>Diana</b>"
	if request.fields["text"] != wantText || request.fields["parse_mode"] != "HTML" {
		t.Errorf("Telegram request fields = %#v, want updated formatted preview", request.fields)
	}
	if request.fields["link_preview_options"] != `{"is_disabled":true}` {
		t.Errorf("link preview options = %q, want disabled", request.fields["link_preview_options"])
	}
	if !strings.Contains(request.fields["reply_markup"], `"web_app":{"url":"https://example.test/miniapp?draft=`+draft.ID+`"}`) {
		t.Errorf("Telegram request reply markup = %q, want the existing Edit button", request.fields["reply_markup"])
	}
}

func TestDraftPreviewUpdaterSkipsDraftWithoutPreviewMessage(t *testing.T) {
	if err := NewDraftPreviewUpdater(nil, nil).UpdateDraftPreview(context.Background(), &models.Draft{}); err != nil {
		t.Fatalf("UpdateDraftPreview returned error: %v, want no-op", err)
	}
}
