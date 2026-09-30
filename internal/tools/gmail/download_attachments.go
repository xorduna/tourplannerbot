package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	downloadGmailAttachmentsToolName       = "download_gmail_attachments"
	maximumAttachmentCount                 = 5
	maximumAttachmentBytes           int64 = 45_000_000
	maximumAttachmentTotalBytes      int64 = 100_000_000
	maximumAttachmentFilenameRunes         = 180
	maximumAttachmentResponseBytes   int64 = ((maximumAttachmentBytes+2)/3)*4 + 1<<20
)

// DownloadAttachmentsTool downloads the named attachments in one Gmail
// message and queues their temporary files for the trusted Telegram handler.
type DownloadAttachmentsTool struct {
	client        *Client
	temporaryRoot string
}

// DownloadAttachmentsConfig controls where temporary attachment files are
// created. An empty root retains the operating-system temporary directory for
// backwards-compatible standalone usage.
type DownloadAttachmentsConfig struct {
	TemporaryRoot string
}

// NewDownloadAttachments creates the Gmail attachment download tool.
func NewDownloadAttachments(client *Client, configurations ...DownloadAttachmentsConfig) (*DownloadAttachmentsTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Gmail client is required")
	}
	if len(configurations) > 1 {
		return nil, fmt.Errorf("at most one Gmail attachment download configuration is allowed")
	}
	temporaryRoot := os.TempDir()
	if len(configurations) == 1 && strings.TrimSpace(configurations[0].TemporaryRoot) != "" {
		temporaryRoot = strings.TrimSpace(configurations[0].TemporaryRoot)
	}
	rootInfo, err := os.Stat(temporaryRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect Gmail attachment temporary root: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("Gmail attachment temporary root must be a directory")
	}
	return &DownloadAttachmentsTool{client: client, temporaryRoot: temporaryRoot}, nil
}

// Definition describes download_gmail_attachments to the LLM. It is an
// intentional delivery action: downloading a file queues it for Telegram.
func (downloadAttachmentsTool *DownloadAttachmentsTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        downloadGmailAttachmentsToolName,
		Description: "Download all named attachments from one Gmail message and send them to the current Telegram conversation as files. Use only when the user explicitly asks to download, retrieve, or send that message's attachments. It supports up to 5 files, 45 MB each, and 100 MB total; temporary copies are deleted after delivery.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message_id": map[string]any{
					"type":        "string",
					"description": "Gmail message ID returned by search_gmail_messages.",
				},
			},
			"required":             []string{"message_id"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "gmail",
	}
}

// Execute retrieves a message's MIME part tree, downloads its named
// attachments, and records their private temporary paths in the trusted
// execution context. Filesystem paths are never returned to the model.
func (downloadAttachmentsTool *DownloadAttachmentsTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	toolExecutionContext, found := tools.ExecutionContextFromContext(ctx)
	if !found {
		return "", fmt.Errorf("Gmail attachment downloads require a trusted tool execution context")
	}
	arguments := struct {
		MessageID string `json:"message_id"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", downloadGmailAttachmentsToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", downloadGmailAttachmentsToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", downloadGmailAttachmentsToolName, err)
	}
	messageID := strings.TrimSpace(arguments.MessageID)
	if !gmailDraftIDPattern.MatchString(messageID) {
		return "", fmt.Errorf("message_id must contain only letters, digits, hyphens, or underscores")
	}

	message, err := downloadAttachmentsTool.fullMessage(ctx, messageID)
	if err != nil {
		return "", err
	}
	if message.ID != messageID {
		return "", fmt.Errorf("Gmail message response ID did not match the requested message")
	}
	attachments := collectMessageAttachments(message.Payload)
	if len(attachments) == 0 {
		return encodeAttachmentDownloadResult(messageID, nil, nil)
	}

	temporaryDirectory := ""
	defer func() {
		if temporaryDirectory != "" {
			// Ownership transfers to the Telegram handler only after every
			// successful download is recorded below.
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()
	queuedFiles := make([]tools.DownloadedFile, 0, min(len(attachments), maximumAttachmentCount))
	results := make([]gmailAttachmentDownloadResult, 0, len(attachments))
	var totalBytes int64
	for attachmentIndex, attachment := range attachments {
		filename := safeAttachmentFilename(attachment.filename, attachmentIndex+1)
		if attachmentIndex >= maximumAttachmentCount {
			results = append(results, gmailAttachmentDownloadResult{Filename: filename, MIMEType: attachment.mimeType, Status: "skipped_file_count_limit"})
			continue
		}
		if attachment.size > maximumAttachmentBytes {
			results = append(results, gmailAttachmentDownloadResult{Filename: filename, MIMEType: attachment.mimeType, Size: attachment.size, Status: "skipped_file_size_limit"})
			continue
		}
		if attachment.size > 0 && totalBytes+attachment.size > maximumAttachmentTotalBytes {
			results = append(results, gmailAttachmentDownloadResult{Filename: filename, MIMEType: attachment.mimeType, Size: attachment.size, Status: "skipped_total_size_limit"})
			continue
		}

		attachmentData, err := downloadAttachmentsTool.attachmentData(ctx, messageID, attachment)
		if err != nil {
			return "", err
		}
		if int64(len(attachmentData)) > maximumAttachmentBytes {
			results = append(results, gmailAttachmentDownloadResult{Filename: filename, MIMEType: attachment.mimeType, Size: int64(len(attachmentData)), Status: "skipped_file_size_limit"})
			continue
		}
		if totalBytes+int64(len(attachmentData)) > maximumAttachmentTotalBytes {
			results = append(results, gmailAttachmentDownloadResult{Filename: filename, MIMEType: attachment.mimeType, Size: int64(len(attachmentData)), Status: "skipped_total_size_limit"})
			continue
		}
		if temporaryDirectory == "" {
			temporaryDirectory, err = os.MkdirTemp(downloadAttachmentsTool.temporaryRoot, "tourplannerbot-gmail-attachments-")
			if err != nil {
				return "", fmt.Errorf("create Gmail attachment temporary directory: %w", err)
			}
		}
		temporaryFile, err := os.CreateTemp(temporaryDirectory, "attachment-")
		if err != nil {
			return "", fmt.Errorf("create Gmail attachment temporary file: %w", err)
		}
		if _, err := temporaryFile.Write(attachmentData); err != nil {
			temporaryFile.Close()
			return "", fmt.Errorf("write Gmail attachment temporary file: %w", err)
		}
		if err := temporaryFile.Close(); err != nil {
			return "", fmt.Errorf("close Gmail attachment temporary file: %w", err)
		}
		attachmentSize := int64(len(attachmentData))
		queuedFiles = append(queuedFiles, tools.DownloadedFile{
			Path:        temporaryFile.Name(),
			Filename:    filename,
			MIMEType:    attachment.mimeType,
			Size:        attachmentSize,
			CleanupPath: temporaryDirectory,
		})
		results = append(results, gmailAttachmentDownloadResult{Filename: filename, MIMEType: attachment.mimeType, Size: attachmentSize, Status: "queued_for_telegram"})
		totalBytes += attachmentSize
	}
	for _, queuedFile := range queuedFiles {
		toolExecutionContext.RecordDownloadedFile(queuedFile)
	}
	if len(queuedFiles) > 0 {
		temporaryDirectory = ""
	}
	return encodeAttachmentDownloadResult(messageID, queuedFiles, results)
}

func (downloadAttachmentsTool *DownloadAttachmentsTool) fullMessage(ctx context.Context, messageID string) (gmailFullMessage, error) {
	responseBody, err := downloadAttachmentsTool.client.doJSON(ctx, http.MethodGet, "/gmail/v1/users/me/messages/"+messageID+"?format=full", nil)
	if err != nil {
		return gmailFullMessage{}, fmt.Errorf("read Gmail message attachments: %w", err)
	}
	var message gmailFullMessage
	if err := json.Unmarshal(responseBody, &message); err != nil {
		return gmailFullMessage{}, fmt.Errorf("decode Gmail message attachment response: %w", err)
	}
	message.ID = strings.TrimSpace(message.ID)
	return message, nil
}

func (downloadAttachmentsTool *DownloadAttachmentsTool) attachmentData(ctx context.Context, messageID string, attachment gmailAttachment) ([]byte, error) {
	if attachment.data != "" {
		return decodeGmailAttachmentData(attachment.data)
	}
	if !gmailDraftIDPattern.MatchString(attachment.id) {
		return nil, fmt.Errorf("Gmail message attachment response included an invalid attachment ID")
	}
	responseBody, err := downloadAttachmentsTool.client.doJSONWithResponseLimit(ctx, http.MethodGet, "/gmail/v1/users/me/messages/"+messageID+"/attachments/"+attachment.id, nil, maximumAttachmentResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("download Gmail attachment: %w", err)
	}
	response := struct {
		Data string `json:"data"`
		Size int64  `json:"size"`
	}{}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode Gmail attachment response: %w", err)
	}
	attachmentData, err := decodeGmailAttachmentData(response.Data)
	if err != nil {
		return nil, err
	}
	if response.Size > 0 && response.Size != int64(len(attachmentData)) {
		return nil, fmt.Errorf("Gmail attachment response size did not match decoded data")
	}
	return attachmentData, nil
}

func decodeGmailAttachmentData(encodedData string) ([]byte, error) {
	if strings.TrimSpace(encodedData) == "" {
		return nil, fmt.Errorf("Gmail attachment response did not include data")
	}
	decodedData, err := base64.RawURLEncoding.DecodeString(encodedData)
	if err == nil {
		return decodedData, nil
	}
	decodedData, paddedError := base64.URLEncoding.DecodeString(encodedData)
	if paddedError != nil {
		return nil, fmt.Errorf("decode Gmail attachment data: %w", err)
	}
	return decodedData, nil
}

type gmailFullMessage struct {
	ID      string           `json:"id"`
	Payload gmailMessagePart `json:"payload"`
}

type gmailMessagePart struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Body     struct {
		AttachmentID string `json:"attachmentId"`
		Data         string `json:"data"`
		Size         int64  `json:"size"`
	} `json:"body"`
	Parts []gmailMessagePart `json:"parts"`
}

type gmailAttachment struct {
	id       string
	filename string
	mimeType string
	size     int64
	data     string
}

func collectMessageAttachments(part gmailMessagePart) []gmailAttachment {
	attachments := make([]gmailAttachment, 0)
	var visit func(gmailMessagePart)
	visit = func(currentPart gmailMessagePart) {
		if strings.TrimSpace(currentPart.Filename) != "" && (strings.TrimSpace(currentPart.Body.AttachmentID) != "" || strings.TrimSpace(currentPart.Body.Data) != "") {
			attachments = append(attachments, gmailAttachment{
				id:       strings.TrimSpace(currentPart.Body.AttachmentID),
				filename: strings.TrimSpace(currentPart.Filename),
				mimeType: strings.TrimSpace(currentPart.MimeType),
				size:     currentPart.Body.Size,
				data:     strings.TrimSpace(currentPart.Body.Data),
			})
		}
		for _, nestedPart := range currentPart.Parts {
			visit(nestedPart)
		}
	}
	visit(part)
	return attachments
}

func safeAttachmentFilename(rawFilename string, attachmentNumber int) string {
	normalizedFilename := strings.ReplaceAll(strings.TrimSpace(rawFilename), `\`, "/")
	normalizedFilename = filepath.Base(normalizedFilename)
	if normalizedFilename == "." || normalizedFilename == "" {
		return fmt.Sprintf("attachment-%d", attachmentNumber)
	}
	if utf8.RuneCountInString(normalizedFilename) > maximumAttachmentFilenameRunes {
		normalizedFilename = string([]rune(normalizedFilename)[:maximumAttachmentFilenameRunes])
	}
	normalizedFilename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == 0 {
			return -1
		}
		return r
	}, normalizedFilename)
	if normalizedFilename == "" || normalizedFilename == "." {
		return fmt.Sprintf("attachment-%d", attachmentNumber)
	}
	return normalizedFilename
}

type gmailAttachmentDownloadResult struct {
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Status   string `json:"status"`
}

func encodeAttachmentDownloadResult(messageID string, queuedFiles []tools.DownloadedFile, results []gmailAttachmentDownloadResult) (string, error) {
	if results == nil {
		results = []gmailAttachmentDownloadResult{}
	}
	result := struct {
		MessageID   string                          `json:"message_id"`
		QueuedCount int                             `json:"queued_count"`
		Attachments []gmailAttachmentDownloadResult `json:"attachments"`
	}{
		MessageID:   messageID,
		QueuedCount: len(queuedFiles),
		Attachments: results,
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode Gmail attachment download result: %w", err)
	}
	return string(encodedResult), nil
}

func min(first int, second int) int {
	if first < second {
		return first
	}
	return second
}
