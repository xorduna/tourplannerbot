package telegram

import (
	"context"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"tourplannerbot/internal/llm"
	applicationTools "tourplannerbot/internal/tools"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// sendDownloadedFiles uploads temporary native-tool outputs as ordinary
// Telegram documents. The context, chat, and topic are trusted values from the
// incoming update; model arguments cannot redirect a file to another chat.
func (telegramHandler *Handler) sendDownloadedFiles(ctx context.Context, telegramBot *bot.Bot, chatID int64, messageThreadID int, downloadedFiles []applicationTools.DownloadedFile) {
	for _, downloadedFile := range downloadedFiles {
		if downloadedFile.AnalysisOnly {
			continue
		}
		if strings.TrimSpace(downloadedFile.Path) == "" || strings.TrimSpace(downloadedFile.Filename) == "" {
			telegramHandler.logger.Warn("skipping invalid downloaded file", "chat_id", chatID, "message_thread_id", messageThreadID)
			continue
		}
		file, err := os.Open(downloadedFile.Path)
		if err != nil {
			telegramHandler.logger.Error("failed to open downloaded file for Telegram", "chat_id", chatID, "message_thread_id", messageThreadID, "filename", downloadedFile.Filename, "error", err)
			continue
		}
		_, sendError := telegramBot.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:          chatID,
			MessageThreadID: messageThreadID,
			Document: &models.InputFileUpload{
				Filename: filepath.Base(downloadedFile.Filename),
				Data:     file,
			},
		})
		closeError := file.Close()
		if sendError != nil {
			telegramHandler.logger.Error("failed to send downloaded file to Telegram", "chat_id", chatID, "message_thread_id", messageThreadID, "filename", downloadedFile.Filename, "error", sendError)
			continue
		}
		if closeError != nil {
			telegramHandler.logger.Warn("failed to close downloaded file after Telegram delivery", "filename", downloadedFile.Filename, "error", closeError)
		}
	}
}

// cleanupDownloadedFiles removes each per-tool temporary directory once the
// send attempt has completed, including error paths. No downloaded attachment
// data is retained on the bot's disk after its response.
func cleanupDownloadedFiles(downloadedFiles []applicationTools.DownloadedFile) {
	cleanupPaths := make(map[string]struct{})
	for _, downloadedFile := range downloadedFiles {
		if isManagedAttachmentTemporaryDirectory(downloadedFile.CleanupPath) {
			cleanupPaths[downloadedFile.CleanupPath] = struct{}{}
		}
	}
	for cleanupPath := range cleanupPaths {
		_ = os.RemoveAll(cleanupPath)
	}
}

// isManagedAttachmentTemporaryDirectory makes cleanup deliberately narrow: it
// can remove only a direct child of the dedicated tmp workspace created by a
// trusted Gmail or Bigin attachment tool.
func isManagedAttachmentTemporaryDirectory(rawPath string) bool {
	cleanedPath := filepath.Clean(strings.TrimSpace(rawPath))
	temporaryDirectoryName := filepath.Base(cleanedPath)
	return (strings.HasPrefix(temporaryDirectoryName, "tourplannerbot-gmail-attachments-") || strings.HasPrefix(temporaryDirectoryName, "tourplannerbot-bigin-attachments-")) && filepath.Base(filepath.Dir(cleanedPath)) == "tmp"
}

// modelFileInputs selects supported document attachments that fit in one
// OpenAI file-input request. The source file remains on disk only until the
// response finishes. File types follow the Responses API input_file support.
func modelFileInputs(downloadedFiles []applicationTools.DownloadedFile) []llm.FileInput {
	fileInputs := make([]llm.FileInput, 0, len(downloadedFiles))
	var totalBytes int64
	for _, downloadedFile := range downloadedFiles {
		if !isSupportedModelFile(downloadedFile) || downloadedFile.Size <= 0 || downloadedFile.Size > llm.MaximumInputFileBytes || totalBytes+downloadedFile.Size > llm.MaximumInputFilesBytes {
			continue
		}
		fileInputs = append(fileInputs, llm.FileInput{
			Path:     downloadedFile.Path,
			Filename: downloadedFile.Filename,
			MIMEType: modelFileMIMEType(downloadedFile),
		})
		totalBytes += downloadedFile.Size
	}
	return fileInputs
}

// modelFileMIMEType preserves the trusted source MIME type and infers one from
// the filename only when the source did not identify a useful content type.
func modelFileMIMEType(downloadedFile applicationTools.DownloadedFile) string {
	mimeType := strings.TrimSpace(downloadedFile.MIMEType)
	if mimeType != "" && !strings.EqualFold(mimeType, "application/octet-stream") {
		return mimeType
	}
	if inferredMIMEType := mime.TypeByExtension(strings.ToLower(filepath.Ext(downloadedFile.Filename))); inferredMIMEType != "" {
		parsedMIMEType, _, _ := mime.ParseMediaType(inferredMIMEType)
		return parsedMIMEType
	}
	return "application/octet-stream"
}

// isSupportedModelFile allows the document, text, presentation, and
// spreadsheet formats accepted by the Responses API as input_file data.
func isSupportedModelFile(downloadedFile applicationTools.DownloadedFile) bool {
	if strings.EqualFold(strings.TrimSpace(downloadedFile.MIMEType), "application/pdf") {
		return true
	}
	switch strings.ToLower(filepath.Ext(downloadedFile.Filename)) {
	case ".pdf", ".txt", ".md", ".json", ".html", ".htm", ".xml", ".doc", ".docx", ".rtf", ".odt", ".ppt", ".pptx", ".csv", ".tsv", ".iif", ".xls", ".xlsx":
		return true
	default:
		return false
	}
}
