package telegram

import (
	"context"
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
// send attempt has completed, including error paths. No downloaded mail data is
// retained on the bot's disk after its response.
func cleanupDownloadedFiles(downloadedFiles []applicationTools.DownloadedFile) {
	cleanupPaths := make(map[string]struct{})
	for _, downloadedFile := range downloadedFiles {
		if isManagedGmailTemporaryDirectory(downloadedFile.CleanupPath) {
			cleanupPaths[downloadedFile.CleanupPath] = struct{}{}
		}
	}
	for cleanupPath := range cleanupPaths {
		_ = os.RemoveAll(cleanupPath)
	}
}

// isManagedGmailTemporaryDirectory makes cleanup deliberately narrow: it can
// remove only a direct child of the dedicated tmp workspace whose name was
// created by DownloadAttachmentsTool.
func isManagedGmailTemporaryDirectory(rawPath string) bool {
	cleanedPath := filepath.Clean(strings.TrimSpace(rawPath))
	return strings.HasPrefix(filepath.Base(cleanedPath), "tourplannerbot-gmail-attachments-") && filepath.Base(filepath.Dir(cleanedPath)) == "tmp"
}

// modelPDFInputs selects PDF attachments that fit in one OpenAI file-input
// request. The source file remains on disk only until the response finishes.
func modelPDFInputs(downloadedFiles []applicationTools.DownloadedFile) []llm.FileInput {
	fileInputs := make([]llm.FileInput, 0, len(downloadedFiles))
	var totalBytes int64
	for _, downloadedFile := range downloadedFiles {
		isPDF := strings.EqualFold(strings.TrimSpace(downloadedFile.MIMEType), "application/pdf") || strings.EqualFold(filepath.Ext(downloadedFile.Filename), ".pdf")
		if !isPDF || downloadedFile.Size <= 0 || downloadedFile.Size > llm.MaximumInputFileBytes || totalBytes+downloadedFile.Size > llm.MaximumInputFilesBytes {
			continue
		}
		fileInputs = append(fileInputs, llm.FileInput{
			Path:     downloadedFile.Path,
			Filename: downloadedFile.Filename,
			MIMEType: "application/pdf",
		})
		totalBytes += downloadedFile.Size
	}
	return fileInputs
}
