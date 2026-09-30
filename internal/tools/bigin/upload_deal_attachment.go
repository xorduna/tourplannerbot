package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	uploadDealAttachmentToolName       = "upload_bigin_deal_attachment"
	maximumBiginAttachmentBytes  int64 = 20 << 20
	maximumBiginAttachmentRunes        = 180
)

// UploadDealAttachmentTool attaches one temporary, trusted conversation file
// to a Bigin pipeline record. It deliberately cannot accept a filesystem path
// from the model.
type UploadDealAttachmentTool struct {
	client        *Client
	temporaryRoot string
}

// NewUploadDealAttachment creates an attachment tool constrained to the
// supplied canonical temporary workspace.
func NewUploadDealAttachment(client *Client, temporaryRoot string) (*UploadDealAttachmentTool, error) {
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
	return &UploadDealAttachmentTool{client: client, temporaryRoot: filepath.Clean(canonicalRoot)}, nil
}

// Definition describes the attachment operation to the LLM.
func (uploadDealAttachmentTool *UploadDealAttachmentTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        uploadDealAttachmentToolName,
		Description: "Attach one file downloaded earlier in this same response to a Zoho Bigin deal (pipeline record). Use only when the user explicitly asks to upload or attach that file to the deal. The filename must exactly match a file returned by download_gmail_attachments. If no file is queued, first search and download the Gmail attachment in this response.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID.",
				},
				"filename": map[string]any{
					"type":        "string",
					"description": "Exact filename returned by download_gmail_attachments in this conversation.",
				},
			},
			"required":             []string{"deal_id", "filename"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute validates that the requested filename resolves to one trusted,
// queued temporary file, then uploads it to Bigin's record attachment API.
func (uploadDealAttachmentTool *UploadDealAttachmentTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	toolExecutionContext, found := tools.ExecutionContextFromContext(ctx)
	if !found {
		return "", fmt.Errorf("Bigin attachment uploads require a trusted tool execution context")
	}
	arguments := struct {
		DealID   string `json:"deal_id"`
		Filename string `json:"filename"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", uploadDealAttachmentToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", uploadDealAttachmentToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", uploadDealAttachmentToolName, err)
	}

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	filename, err := validBiginAttachmentFilename(arguments.Filename)
	if err != nil {
		return "", err
	}
	queuedFile, found := queuedBiginAttachment(toolExecutionContext.DownloadedFiles(), filename)
	if !found {
		return "", fmt.Errorf("Bigin attachment upload: downloaded file was not found in this response; first search and download the Gmail attachment, then retry with its returned filename")
	}
	fileInfo, err := uploadDealAttachmentTool.trustedAttachmentFile(queuedFile.Path)
	if err != nil {
		return "", err
	}
	if fileInfo.Size() > maximumBiginAttachmentBytes {
		return "", fmt.Errorf("Bigin attachment upload: file exceeds the %d MB limit", maximumBiginAttachmentBytes>>20)
	}

	responseBody, err := uploadDealAttachmentTool.client.postMultipartFile(ctx, "/bigin/v2/Pipelines/"+dealID+"/Attachments", queuedFile.Path, filename)
	if err != nil {
		return "", fmt.Errorf("upload Bigin deal attachment: %w", err)
	}
	return string(responseBody), nil
}

func (uploadDealAttachmentTool *UploadDealAttachmentTool) trustedAttachmentFile(rawPath string) (os.FileInfo, error) {
	cleanedPath := filepath.Clean(strings.TrimSpace(rawPath))
	fileInfo, err := os.Lstat(cleanedPath)
	if err != nil {
		return nil, fmt.Errorf("inspect Bigin attachment file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("Bigin attachment upload: source must be a regular file")
	}
	resolvedPath, err := filepath.EvalSymlinks(cleanedPath)
	if err != nil {
		return nil, fmt.Errorf("resolve Bigin attachment file: %w", err)
	}
	if !pathWithin(uploadDealAttachmentTool.temporaryRoot, resolvedPath) {
		return nil, fmt.Errorf("Bigin attachment upload: file is outside the managed workspace")
	}
	return fileInfo, nil
}

func queuedBiginAttachment(queuedFiles []tools.DownloadedFile, filename string) (tools.DownloadedFile, bool) {
	var matchedFile tools.DownloadedFile
	for _, queuedFile := range queuedFiles {
		if queuedFile.Filename != filename {
			continue
		}
		if matchedFile.Path != "" {
			return tools.DownloadedFile{}, false
		}
		matchedFile = queuedFile
	}
	return matchedFile, matchedFile.Path != ""
}

func validBiginAttachmentFilename(rawFilename string) (string, error) {
	filename := strings.TrimSpace(rawFilename)
	if filename == "" {
		return "", fmt.Errorf("filename must not be empty")
	}
	if !utf8.ValidString(filename) || utf8.RuneCountInString(filename) > maximumBiginAttachmentRunes {
		return "", fmt.Errorf("filename must be valid UTF-8 and at most %d characters", maximumBiginAttachmentRunes)
	}
	if filename == "." || filename == ".." || filepath.Base(filename) != filename || strings.ContainsAny(filename, `\\/`) || strings.IndexFunc(filename, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("filename must be a filename, not a path")
	}
	if strings.EqualFold(filepath.Ext(filename), ".exe") {
		return "", fmt.Errorf("Bigin attachment upload does not support .exe files")
	}
	return filename, nil
}

func pathWithin(root string, candidate string) bool {
	relativePath, err := filepath.Rel(root, candidate)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}
