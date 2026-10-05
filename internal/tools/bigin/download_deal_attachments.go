package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	downloadDealAttachmentsToolName              = "download_bigin_deal_attachments"
	maximumBiginDownloadBytes              int64 = 45_000_000
	maximumBiginDownloadFilenameRunes            = 180
	maximumBiginAttachmentIDLength               = 64
	defaultBiginDownloadFilenameMIMEType         = "application/octet-stream"
)

// DownloadDealAttachmentsTool lists the attachments related to one Bigin
// pipeline record and, when an attachment ID is supplied, downloads that file
// and queues it for delivery to the current Telegram conversation. Filesystem
// paths are derived from the trusted execution context, never from the model.
type DownloadDealAttachmentsTool struct {
	client        *Client
	temporaryRoot string
}

// NewDownloadDealAttachments creates the Bigin attachment download tool
// constrained to the supplied temporary workspace.
func NewDownloadDealAttachments(client *Client, temporaryRoot string) (*DownloadDealAttachmentsTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	root := strings.TrimSpace(temporaryRoot)
	if root == "" {
		return nil, fmt.Errorf("Bigin attachment temporary root is required")
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect Bigin attachment temporary root: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("Bigin attachment temporary root must be a directory")
	}
	return &DownloadDealAttachmentsTool{client: client, temporaryRoot: root}, nil
}

// Definition describes download_bigin_deal_attachments to the LLM. Listing is a
// read-only inspection; downloading is an intentional delivery action that
// sends the chosen file to the Telegram conversation.
func (downloadDealAttachmentsTool *DownloadDealAttachmentsTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        downloadDealAttachmentsToolName,
		Description: "List or download the files attached to one Zoho Bigin deal (pipeline record). Pass attachment_id as null to list the deal's attachments with their id, file name, and size. Use an attachment_id returned by that list to download that single file and send it to the current Telegram conversation; download only when the user explicitly asks to download, retrieve, or send that file. Downloads support one file up to 45 MB; the temporary copy is deleted after delivery.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID.",
				},
				"attachment_id": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Attachment ID returned by an earlier list call, or null to list the deal's attachments.",
				},
			},
			"required":             []string{"deal_id", "attachment_id"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute lists the deal's attachments, or downloads one of them and records
// its private temporary path in the trusted execution context. Filesystem
// paths are never returned to the model.
func (downloadDealAttachmentsTool *DownloadDealAttachmentsTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID       string          `json:"deal_id"`
		AttachmentID json.RawMessage `json:"attachment_id"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", downloadDealAttachmentsToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", downloadDealAttachmentsToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", downloadDealAttachmentsToolName, err)
	}

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	if arguments.AttachmentID == nil {
		return "", fmt.Errorf("attachment_id is required; use null to list attachments")
	}

	attachmentID := ""
	if !bytes.Equal(bytes.TrimSpace(arguments.AttachmentID), []byte("null")) {
		if err := json.Unmarshal(arguments.AttachmentID, &attachmentID); err != nil {
			return "", fmt.Errorf("attachment_id must be a string or null: %w", err)
		}
		attachmentID = strings.TrimSpace(attachmentID)
		if !biginRecordIDPattern.MatchString(attachmentID) || len(attachmentID) > maximumBiginAttachmentIDLength {
			return "", fmt.Errorf("attachment_id must contain only digits")
		}
	}

	attachments, err := downloadDealAttachmentsTool.listAttachments(ctx, dealID)
	if err != nil {
		return "", err
	}

	if attachmentID == "" {
		return encodeDealAttachmentList(dealID, attachments)
	}
	return downloadDealAttachmentsTool.downloadAttachment(ctx, dealID, attachmentID, attachments)
}

// listAttachments retrieves the deal's attachment related list and returns its
// sanitized metadata.
func (downloadDealAttachmentsTool *DownloadDealAttachmentsTool) listAttachments(ctx context.Context, dealID string) ([]biginAttachmentMetadata, error) {
	responseBody, err := downloadDealAttachmentsTool.client.get(ctx, "/bigin/v2/Pipelines/"+dealID+"/Attachments")
	if err != nil {
		return nil, fmt.Errorf("list Bigin deal attachments: %w", err)
	}
	response := struct {
		Data []struct {
			ID         string          `json:"id"`
			FileName   string          `json:"File_Name"`
			Size       json.RawMessage `json:"Size"`
			DollarSize json.RawMessage `json:"$size"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode Bigin deal attachment list: %w", err)
	}
	attachments := make([]biginAttachmentMetadata, 0, len(response.Data))
	for attachmentIndex, rawAttachment := range response.Data {
		identifier := strings.TrimSpace(rawAttachment.ID)
		if identifier == "" {
			continue
		}
		size := parseBiginAttachmentSize(rawAttachment.Size)
		if size == 0 {
			size = parseBiginAttachmentSize(rawAttachment.DollarSize)
		}
		attachments = append(attachments, biginAttachmentMetadata{
			ID:       identifier,
			FileName: safeBiginDownloadFilename(rawAttachment.FileName, attachmentIndex+1),
			Size:     size,
		})
	}
	return attachments, nil
}

// downloadAttachment downloads one listed attachment to a private temporary
// file and queues it for the Telegram handler.
func (downloadDealAttachmentsTool *DownloadDealAttachmentsTool) downloadAttachment(ctx context.Context, dealID string, attachmentID string, attachments []biginAttachmentMetadata) (string, error) {
	toolExecutionContext, found := tools.ExecutionContextFromContext(ctx)
	if !found {
		return "", fmt.Errorf("Bigin attachment downloads require a trusted tool execution context")
	}

	var selectedAttachment biginAttachmentMetadata
	for _, attachment := range attachments {
		if attachment.ID == attachmentID {
			selectedAttachment = attachment
			break
		}
	}
	if selectedAttachment.ID == "" {
		return "", fmt.Errorf("Bigin attachment download: attachment was not found on this deal; list the deal's attachments first, then retry with a returned attachment_id")
	}
	if selectedAttachment.Size > maximumBiginDownloadBytes {
		return "", fmt.Errorf("Bigin attachment download: file exceeds the %d MB limit", maximumBiginDownloadBytes/1_000_000)
	}

	attachmentData, err := downloadDealAttachmentsTool.client.getBinary(ctx, "/bigin/v2/Pipelines/"+dealID+"/Attachments/"+attachmentID, maximumBiginDownloadBytes+1)
	if err != nil {
		return "", fmt.Errorf("download Bigin deal attachment: %w", err)
	}
	if int64(len(attachmentData)) > maximumBiginDownloadBytes {
		return "", fmt.Errorf("Bigin attachment download: file exceeds the %d MB limit", maximumBiginDownloadBytes/1_000_000)
	}
	if len(attachmentData) == 0 {
		return "", fmt.Errorf("Bigin attachment download: downloaded file was empty")
	}

	temporaryDirectory, err := os.MkdirTemp(downloadDealAttachmentsTool.temporaryRoot, "tourplannerbot-bigin-attachments-")
	if err != nil {
		return "", fmt.Errorf("create Bigin attachment temporary directory: %w", err)
	}
	ownershipTransferred := false
	defer func() {
		if !ownershipTransferred {
			// Ownership transfers to the Telegram handler only after the file
			// is recorded in the execution context below.
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()
	temporaryFile, err := os.CreateTemp(temporaryDirectory, "attachment-")
	if err != nil {
		return "", fmt.Errorf("create Bigin attachment temporary file: %w", err)
	}
	if _, err := temporaryFile.Write(attachmentData); err != nil {
		temporaryFile.Close()
		return "", fmt.Errorf("write Bigin attachment temporary file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return "", fmt.Errorf("close Bigin attachment temporary file: %w", err)
	}

	queuedFile := tools.DownloadedFile{
		Path:        temporaryFile.Name(),
		Filename:    selectedAttachment.FileName,
		MIMEType:    defaultBiginDownloadFilenameMIMEType,
		Size:        int64(len(attachmentData)),
		CleanupPath: temporaryDirectory,
	}
	toolExecutionContext.RecordDownloadedFile(queuedFile)
	ownershipTransferred = true

	return encodeDealAttachmentDownloadResult(dealID, queuedFile)
}

// biginAttachmentMetadata is the sanitized subset of one Bigin attachment the
// tool exposes to the model and uses to drive a download.
type biginAttachmentMetadata struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size,omitempty"`
}

// parseBiginAttachmentSize reads a Bigin attachment size that may be encoded as
// a JSON number or a quoted string, returning zero when it is absent or
// invalid.
func parseBiginAttachmentSize(rawSize json.RawMessage) int64 {
	trimmed := bytes.TrimSpace(rawSize)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0
	}
	var numericSize int64
	if err := json.Unmarshal(trimmed, &numericSize); err == nil {
		if numericSize < 0 {
			return 0
		}
		return numericSize
	}
	var stringSize string
	if err := json.Unmarshal(trimmed, &stringSize); err != nil {
		return 0
	}
	parsedSize, err := strconv.ParseInt(strings.TrimSpace(stringSize), 10, 64)
	if err != nil || parsedSize < 0 {
		return 0
	}
	return parsedSize
}

// safeBiginDownloadFilename normalizes a Bigin-provided file name into a plain
// filename safe to write inside the managed workspace and send to Telegram.
func safeBiginDownloadFilename(rawFilename string, attachmentNumber int) string {
	normalizedFilename := strings.ReplaceAll(strings.TrimSpace(rawFilename), `\`, "/")
	normalizedFilename = filepath.Base(normalizedFilename)
	if normalizedFilename == "." || normalizedFilename == "" {
		return fmt.Sprintf("attachment-%d", attachmentNumber)
	}
	if !utf8.ValidString(normalizedFilename) {
		return fmt.Sprintf("attachment-%d", attachmentNumber)
	}
	if utf8.RuneCountInString(normalizedFilename) > maximumBiginDownloadFilenameRunes {
		normalizedFilename = string([]rune(normalizedFilename)[:maximumBiginDownloadFilenameRunes])
	}
	normalizedFilename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == 0 {
			return -1
		}
		return r
	}, normalizedFilename)
	if normalizedFilename == "" || normalizedFilename == "." || normalizedFilename == ".." {
		return fmt.Sprintf("attachment-%d", attachmentNumber)
	}
	return normalizedFilename
}

func encodeDealAttachmentList(dealID string, attachments []biginAttachmentMetadata) (string, error) {
	if attachments == nil {
		attachments = []biginAttachmentMetadata{}
	}
	result := struct {
		DealID      string                    `json:"deal_id"`
		Count       int                       `json:"count"`
		Attachments []biginAttachmentMetadata `json:"attachments"`
	}{
		DealID:      dealID,
		Count:       len(attachments),
		Attachments: attachments,
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode Bigin deal attachment list: %w", err)
	}
	return string(encodedResult), nil
}

func encodeDealAttachmentDownloadResult(dealID string, queuedFile tools.DownloadedFile) (string, error) {
	result := struct {
		DealID   string `json:"deal_id"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		Status   string `json:"status"`
	}{
		DealID:   dealID,
		Filename: queuedFile.Filename,
		Size:     queuedFile.Size,
		Status:   "queued_for_telegram",
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode Bigin deal attachment download result: %w", err)
	}
	return string(encodedResult), nil
}
