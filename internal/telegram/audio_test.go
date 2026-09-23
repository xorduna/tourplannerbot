package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// TestDownloadTelegramVoiceNote verifies Telegram metadata and file content become an audio input.
func TestDownloadTelegramVoiceNote(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/getFile"):
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"ok":true,"result":{"file_id":"voice-file","file_unique_id":"unique","file_size":11,"file_path":"voice/test.ogg"}}`))
		case strings.Contains(request.URL.Path, "/file/bot"):
			_, _ = responseWriter.Write([]byte("voice bytes"))
		default:
			http.NotFound(responseWriter, request)
		}
	}))
	defer testServer.Close()

	telegramBot, err := bot.New(
		"test-token",
		bot.WithSkipGetMe(),
		bot.WithServerURL(testServer.URL),
	)
	if err != nil {
		t.Fatalf("create Telegram bot: %v", err)
	}
	handler := &Handler{
		logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		telegramFileHTTPClient: &http.Client{Timeout: time.Second},
	}
	audio, err := handler.downloadTelegramAudio(context.Background(), telegramBot, &models.Message{
		Voice: &models.Voice{FileID: "voice-file", MimeType: "audio/ogg", FileSize: 11},
	})
	if err != nil {
		t.Fatalf("downloadTelegramAudio returned an error: %v", err)
	}
	if string(audio.Content) != "voice bytes" || audio.Filename != "voice.ogg" || audio.MIMEType != "audio/ogg" {
		t.Errorf("audio = %#v", audio)
	}
}

// TestExtractTelegramAudioMetadataSupportsUploadedAudio verifies user-supplied filenames are sanitized.
func TestExtractTelegramAudioMetadataSupportsUploadedAudio(t *testing.T) {
	t.Parallel()

	metadata, err := extractTelegramAudioMetadata(&models.Message{Audio: &models.Audio{
		FileID:   "audio-file",
		FileName: "../../route-note.m4a",
		MimeType: "audio/m4a",
		FileSize: 42,
	}})
	if err != nil {
		t.Fatalf("extractTelegramAudioMetadata returned an error: %v", err)
	}
	if metadata.filename != "route-note.m4a" || metadata.fileID != "audio-file" || metadata.fileSize != 42 {
		t.Errorf("metadata = %#v", metadata)
	}
}

// TestDownloadTelegramAudioRejectsOversizedMetadata verifies large files fail before Telegram is contacted.
func TestDownloadTelegramAudioRejectsOversizedMetadata(t *testing.T) {
	t.Parallel()

	handler := &Handler{}
	_, err := handler.downloadTelegramAudio(context.Background(), nil, &models.Message{
		Voice: &models.Voice{FileID: "large-voice", FileSize: maximumTelegramAudioBytes + 1},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("downloadTelegramAudio error = %v, want size-limit error", err)
	}
}

// TestAudioConfirmationTextProducesExpectedFirstMessage verifies the acknowledgement is deterministic.
func TestAudioConfirmationTextProducesExpectedFirstMessage(t *testing.T) {
	t.Parallel()

	actualText := audioConfirmationText("vols anar dimecres a Tarragona")
	expectedText := "🎙️ M’has dit que vols anar dimecres a Tarragona."
	if actualText != expectedText {
		t.Fatalf("audioConfirmationText = %q, want %q", actualText, expectedText)
	}
}
