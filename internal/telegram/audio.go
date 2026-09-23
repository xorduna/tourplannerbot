package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"tourplannerbot/internal/audioinput"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const maximumTelegramAudioBytes int64 = 25_000_000

var supportedAudioFilenameExtensions = map[string]struct{}{
	".flac": {},
	".m4a":  {},
	".mp3":  {},
	".mp4":  {},
	".mpeg": {},
	".mpga": {},
	".ogg":  {},
	".wav":  {},
	".webm": {},
}

var audioFilenameExtensionByMIMEType = map[string]string{
	"audio/flac":      ".flac",
	"audio/m4a":       ".m4a",
	"audio/mp4":       ".m4a",
	"audio/mpeg":      ".mp3",
	"audio/ogg":       ".ogg",
	"audio/wav":       ".wav",
	"audio/webm":      ".webm",
	"audio/x-m4a":     ".m4a",
	"audio/x-wav":     ".wav",
	"video/mp4":       ".mp4",
	"video/webm":      ".webm",
	"application/ogg": ".ogg",
}

// telegramAudioMetadata contains the safe download metadata exposed by one Telegram audio message.
type telegramAudioMetadata struct {
	fileID   string
	filename string
	mimeType string
	fileSize int64
}

// hasTelegramAudio reports whether a message contains a voice note or audio attachment.
func hasTelegramAudio(message *models.Message) bool {
	return message != nil && (message.Voice != nil || message.Audio != nil)
}

// extractTelegramAudioMetadata normalizes Telegram voice notes and audio files into one representation.
func extractTelegramAudioMetadata(message *models.Message) (telegramAudioMetadata, error) {
	if message == nil {
		return telegramAudioMetadata{}, fmt.Errorf("Telegram message is nil")
	}
	if message.Voice != nil {
		mimeType := strings.TrimSpace(message.Voice.MimeType)
		if mimeType == "" {
			mimeType = "audio/ogg"
		}
		return telegramAudioMetadata{
			fileID:   message.Voice.FileID,
			filename: normalizedAudioFilename("voice", mimeType),
			mimeType: mimeType,
			fileSize: message.Voice.FileSize,
		}, nil
	}
	if message.Audio != nil {
		mimeType := strings.TrimSpace(message.Audio.MimeType)
		return telegramAudioMetadata{
			fileID:   message.Audio.FileID,
			filename: normalizedAudioFilename(message.Audio.FileName, mimeType),
			mimeType: mimeType,
			fileSize: message.Audio.FileSize,
		}, nil
	}
	return telegramAudioMetadata{}, fmt.Errorf("Telegram message does not contain audio")
}

// normalizedAudioFilename keeps supported extensions and derives a safe fallback from the MIME type.
func normalizedAudioFilename(filename string, mimeType string) string {
	safeFilename := filepath.Base(strings.TrimSpace(filename))
	filenameExtension := strings.ToLower(filepath.Ext(safeFilename))
	if _, isSupported := supportedAudioFilenameExtensions[filenameExtension]; isSupported {
		return safeFilename
	}

	fallbackExtension := audioFilenameExtensionByMIMEType[strings.ToLower(strings.TrimSpace(mimeType))]
	if fallbackExtension == "" {
		fallbackExtension = ".ogg"
	}
	filenameStem := strings.TrimSuffix(safeFilename, filepath.Ext(safeFilename))
	if filenameStem == "" || filenameStem == "." {
		filenameStem = "audio"
	}
	return filenameStem + fallbackExtension
}

// downloadTelegramAudio downloads one authorized recording with a strict in-memory size limit.
func (telegramHandler *Handler) downloadTelegramAudio(ctx context.Context, telegramBot *bot.Bot, message *models.Message) (audioinput.Audio, error) {
	audioMetadata, err := extractTelegramAudioMetadata(message)
	if err != nil {
		return audioinput.Audio{}, err
	}
	if audioMetadata.fileID == "" {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio is missing its file ID")
	}
	if audioMetadata.fileSize > maximumTelegramAudioBytes {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio exceeds the %d-byte limit", maximumTelegramAudioBytes)
	}

	telegramFile, err := telegramBot.GetFile(ctx, &bot.GetFileParams{FileID: audioMetadata.fileID})
	if err != nil {
		return audioinput.Audio{}, fmt.Errorf("get Telegram audio file: %w", err)
	}
	if telegramFile.FilePath == "" {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio file path is empty")
	}
	if telegramFile.FileSize > maximumTelegramAudioBytes {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio exceeds the %d-byte limit", maximumTelegramAudioBytes)
	}

	downloadRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, telegramBot.FileDownloadLink(telegramFile), nil)
	if err != nil {
		return audioinput.Audio{}, fmt.Errorf("create Telegram audio download request")
	}
	downloadHTTPClient := telegramHandler.telegramFileHTTPClient
	if downloadHTTPClient == nil {
		downloadHTTPClient = http.DefaultClient
	}
	downloadResponse, err := downloadHTTPClient.Do(downloadRequest)
	if err != nil {
		// Do not wrap the URL error because the Telegram download URL contains the bot token.
		return audioinput.Audio{}, fmt.Errorf("Telegram audio download request failed")
	}
	defer downloadResponse.Body.Close()
	if downloadResponse.StatusCode < http.StatusOK || downloadResponse.StatusCode >= http.StatusMultipleChoices {
		return audioinput.Audio{}, fmt.Errorf("download Telegram audio: unexpected HTTP status %d", downloadResponse.StatusCode)
	}
	if downloadResponse.ContentLength > maximumTelegramAudioBytes {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio exceeds the %d-byte limit", maximumTelegramAudioBytes)
	}

	limitedAudioReader := io.LimitReader(downloadResponse.Body, maximumTelegramAudioBytes+1)
	audioContent, err := io.ReadAll(limitedAudioReader)
	if err != nil {
		return audioinput.Audio{}, fmt.Errorf("read Telegram audio: %w", err)
	}
	if int64(len(audioContent)) > maximumTelegramAudioBytes {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio exceeds the %d-byte limit", maximumTelegramAudioBytes)
	}
	if len(audioContent) == 0 {
		return audioinput.Audio{}, fmt.Errorf("Telegram audio download is empty")
	}

	return audioinput.Audio{
		Content:  audioContent,
		Filename: audioMetadata.filename,
		MIMEType: audioMetadata.mimeType,
	}, nil
}

// audioConfirmationText formats the permanent first reply without persisting it as conversation context.
func audioConfirmationText(confirmationSummary string) string {
	trimmedSummary := strings.TrimSpace(confirmationSummary)
	confirmationText := "🎙️ M’has dit que " + trimmedSummary
	if strings.HasSuffix(trimmedSummary, ".") || strings.HasSuffix(trimmedSummary, "?") || strings.HasSuffix(trimmedSummary, "!") || strings.HasSuffix(trimmedSummary, "…") {
		return confirmationText
	}
	return confirmationText + "."
}
