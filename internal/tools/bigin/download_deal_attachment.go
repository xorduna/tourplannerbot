package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"tourplannerbot/internal/tools"
)

const (
	downloadDealAttachmentToolName              = "download_bigin_deal_attachment"
	maximumDownloadedBiginAttachmentBytes int64 = 20 << 20
)

// DownloadDealAttachmentTool lists or downloads attachments belonging to one
// Bigin pipeline record. Downloaded files are kept only for the active tool
// execution and are never exposed as filesystem paths to the model.
type DownloadDealAttachmentTool struct {
	client        *Client
	temporaryRoot string
}

type biginAttachmentListEnvelope struct {
	Data []biginDealAttachment `json:"data"`
}

type biginDealAttachment struct {
	ID       string `json:"id"`
	FileName string `json:"File_Name"`
	FileID   string `json:"$file_id"`
}

// NewDownloadDealAttachment creates a Bigin attachment reader limited to the
// supplied managed temporary workspace.
func NewDownloadDealAttachment(client *Client, temporaryRoot string) (*DownloadDealAttachmentTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	root := strings.TrimSpace(temporaryRoot)
	if root == "" {
		return nil, fmt.Errorf("Bigin attachment temporary root is required")
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Bigin attachment temporary root: %w", err)
	}
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect Bigin attachment temporary root: %w", err)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("Bigin attachment temporary root must be a directory")
	}
	return &DownloadDealAttachmentTool{client: client, temporaryRoot: filepath.Clean(canonicalRoot)}, nil
}

// Definition describes a two-step attachment operation. Listing first gives
// the model attachment IDs and names without downloading user data.
func (downloadDealAttachmentTool *DownloadDealAttachmentTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        downloadDealAttachmentToolName,
		Description: "List or download attachments from a Zoho Bigin deal (pipeline record). Pass attachment_id as null to list available attachments. Then pass an attachment ID from that list only when the user asks to inspect, analyse, retrieve, or send that attachment. Supported documents are made available to analyse in the current response. Set send_to_telegram to true only when the user explicitly asks to receive the file; otherwise it remains temporary and is not sent.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID.",
				},
				"attachment_id": map[string]any{
					"type":        []string{"string", "null"},
					"description": "An attachment ID returned by the list operation, or null to list attachments.",
				},
				"send_to_telegram": map[string]any{
					"type":        "boolean",
					"description": "Send the downloaded file to the current Telegram conversation. Set true only on the user's explicit request. Defaults to false.",
				},
			},
			"required":             []string{"deal_id", "attachment_id"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute lists attachment metadata or validates, downloads, and queues one
// attachment for temporary model analysis and Telegram delivery.
func (downloadDealAttachmentTool *DownloadDealAttachmentTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID         string  `json:"deal_id"`
		AttachmentID   *string `json:"attachment_id"`
		SendToTelegram bool    `json:"send_to_telegram"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", downloadDealAttachmentToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", downloadDealAttachmentToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", downloadDealAttachmentToolName, err)
	}
	var rawArgumentObject map[string]json.RawMessage
	if err := json.Unmarshal(rawArguments, &rawArgumentObject); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", downloadDealAttachmentToolName, err)
	}
	if _, found := rawArgumentObject["attachment_id"]; !found {
		return "", fmt.Errorf("attachment_id is required and must be a string or null")
	}
	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	var toolExecutionContext *tools.ExecutionContext
	if arguments.AttachmentID != nil {
		var found bool
		toolExecutionContext, found = tools.ExecutionContextFromContext(ctx)
		if !found {
			return "", fmt.Errorf("Bigin attachment downloads require a trusted tool execution context")
		}
	}

	attachments, listResponse, err := downloadDealAttachmentTool.list(ctx, dealID)
	if err != nil {
		return "", err
	}
	if arguments.AttachmentID == nil {
		return string(listResponse), nil
	}
	attachmentID := strings.TrimSpace(*arguments.AttachmentID)
	if !biginRecordIDPattern.MatchString(attachmentID) {
		return "", fmt.Errorf("attachment_id must contain only digits")
	}
	attachment, found := findBiginDealAttachment(attachments, attachmentID)
	if !found {
		return "", fmt.Errorf("Bigin attachment %s was not found on deal %s; list the deal attachments and use a returned attachment ID", attachmentID, dealID)
	}
	temporaryDirectory, err := os.MkdirTemp(downloadDealAttachmentTool.temporaryRoot, "tourplannerbot-bigin-attachments-")
	if err != nil {
		return "", fmt.Errorf("create Bigin attachment temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryDirectory) }()
	temporaryFile, err := os.CreateTemp(temporaryDirectory, "attachment-")
	if err != nil {
		return "", fmt.Errorf("create Bigin attachment temporary file: %w", err)
	}
	contentType, attachmentSize, downloadError := downloadDealAttachmentTool.client.download(ctx, "/bigin/v2/Pipelines/"+dealID+"/Attachments/"+attachmentID, temporaryFile, maximumDownloadedBiginAttachmentBytes)
	closeError := temporaryFile.Close()
	if downloadError != nil {
		return "", downloadError
	}
	if closeError != nil {
		return "", fmt.Errorf("close Bigin attachment temporary file: %w", closeError)
	}
	filename := uniqueBiginAttachmentFilename(attachment.FileName, attachmentID, toolExecutionContext.DownloadedFiles())
	mimeType := normalizedBiginAttachmentMIMEType(contentType, filename)
	toolExecutionContext.RecordDownloadedFile(tools.DownloadedFile{
		Path:         temporaryFile.Name(),
		Filename:     filename,
		MIMEType:     mimeType,
		Size:         attachmentSize,
		CleanupPath:  temporaryDirectory,
		AnalysisOnly: !arguments.SendToTelegram,
	})
	// Ownership has transferred to the Telegram handler on successful queueing.
	temporaryDirectory = ""
	result, err := json.Marshal(struct {
		DealID         string `json:"deal_id"`
		AttachmentID   string `json:"attachment_id"`
		Filename       string `json:"filename"`
		MIMEType       string `json:"mime_type"`
		Size           int64  `json:"size"`
		SentToTelegram bool   `json:"sent_to_telegram"`
		Status         string `json:"status"`
	}{dealID, attachmentID, filename, mimeType, attachmentSize, arguments.SendToTelegram, "queued_for_analysis"})
	if err != nil {
		return "", fmt.Errorf("encode Bigin attachment download result: %w", err)
	}
	return string(result), nil
}

// list reads Bigin's attachment related list and preserves its exact response
// for list calls, while decoding only the fields required for safe downloads.
func (downloadDealAttachmentTool *DownloadDealAttachmentTool) list(ctx context.Context, dealID string) ([]biginDealAttachment, []byte, error) {
	responseBody, err := downloadDealAttachmentTool.client.get(ctx, "/bigin/v2/Pipelines/"+dealID+"/Attachments")
	if err != nil {
		return nil, nil, fmt.Errorf("list Bigin deal attachments: %w", err)
	}
	var responseEnvelope biginAttachmentListEnvelope
	if err := json.Unmarshal(responseBody, &responseEnvelope); err != nil {
		return nil, nil, fmt.Errorf("decode Bigin deal attachment list: %w", err)
	}
	return responseEnvelope.Data, responseBody, nil
}

func findBiginDealAttachment(attachments []biginDealAttachment, attachmentID string) (biginDealAttachment, bool) {
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.ID) == attachmentID {
			return attachment, true
		}
	}
	return biginDealAttachment{}, false
}

func uniqueBiginAttachmentFilename(rawFilename string, attachmentID string, queuedFiles []tools.DownloadedFile) string {
	filename, err := validBiginAttachmentFilename(rawFilename)
	if err != nil {
		filename = "bigin-attachment-" + attachmentID
	}
	for _, queuedFile := range queuedFiles {
		if queuedFile.Filename != filename {
			continue
		}
		extension := filepath.Ext(filename)
		basename := strings.TrimSuffix(filename, extension)
		filename = basename + "-" + attachmentID + extension
		break
	}
	return filename
}

func normalizedBiginAttachmentMIMEType(rawContentType string, filename string) string {
	contentType, _, err := mime.ParseMediaType(rawContentType)
	if err == nil && strings.TrimSpace(contentType) != "" && !strings.EqualFold(contentType, "application/octet-stream") {
		return contentType
	}
	if inferredContentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); inferredContentType != "" {
		contentType, _, _ = mime.ParseMediaType(inferredContentType)
		return contentType
	}
	return "application/octet-stream"
}
