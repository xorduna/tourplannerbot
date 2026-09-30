// Package filesystem implements safe operations on files queued for delivery
// to the current Telegram conversation.
package filesystem

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
	toolName             = "filesystem"
	renameFileCommand    = "rename_file"
	maximumFilenameRunes = 180
)

// Config identifies the dedicated local workspace in which filesystem commands
// are allowed to operate.
type Config struct {
	Root string
}

// Tool provides safe, extensible operations on downloaded files. It never
// receives or exposes server paths: it can operate only on files queued by a
// trusted tool invocation in the active conversation and stored under root.
type Tool struct {
	root string
}

// New creates the workspace if needed and returns a filesystem tool confined
// to that canonical directory.
func New(configuration Config) (*Tool, error) {
	root, err := workspaceRoot(configuration.Root)
	if err != nil {
		return nil, err
	}
	return &Tool{root: root}, nil
}

// Root returns the canonical workspace path for trusted native tools that
// create files to be managed by this tool.
func (tool *Tool) Root() string {
	return tool.root
}

// Definition describes the first filesystem command. Future commands can be
// added to the command enum without changing the tool's public name.
func (tool *Tool) Definition() tools.Definition {
	return tools.Definition{
		Name:        toolName,
		Description: "Manage files already downloaded in the current conversation and stored in the dedicated local workspace. The rename_file command physically renames a queued downloaded file before Telegram sends it. It cannot access files outside that workspace.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"enum":        []string{renameFileCommand},
					"description": "Filesystem command to execute.",
				},
				"filename": map[string]any{
					"type":        "string",
					"description": "Exact filename returned by the tool that downloaded the file in this conversation.",
				},
				"new_filename": map[string]any{
					"type":        "string",
					"description": "New filename to use for the queued file when it is delivered to Telegram.",
				},
			},
			"required":             []string{"command", "filename", "new_filename"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "filesystem",
	}
}

// Execute physically renames one queued delivery file after validating that
// neither name can be interpreted as a path outside the managed workspace.
func (tool *Tool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	toolExecutionContext, found := tools.ExecutionContextFromContext(ctx)
	if !found {
		return "", fmt.Errorf("filesystem operations require a trusted tool execution context")
	}
	arguments := struct {
		Command     string `json:"command"`
		Filename    string `json:"filename"`
		NewFilename string `json:"new_filename"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", toolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", toolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", toolName, err)
	}
	if strings.TrimSpace(arguments.Command) != renameFileCommand {
		return "", fmt.Errorf("command must be %s", renameFileCommand)
	}
	filename, err := safeFilename(arguments.Filename, "filename")
	if err != nil {
		return "", err
	}
	newFilename, err := safeFilename(arguments.NewFilename, "new_filename")
	if err != nil {
		return "", err
	}
	if filename == newFilename {
		return "", fmt.Errorf("new_filename must differ from filename")
	}
	queuedFiles := toolExecutionContext.DownloadedFiles()
	matchingFile, found := queuedDownloadedFile(queuedFiles, filename)
	if !found {
		return "", fmt.Errorf("rename downloaded file: downloaded file was not found in this conversation")
	}
	for _, queuedFile := range queuedFiles {
		if queuedFile.Filename == newFilename {
			return "", fmt.Errorf("rename downloaded file: another downloaded file already has the requested filename")
		}
	}
	if !tool.contains(matchingFile.Path) || !tool.contains(matchingFile.CleanupPath) {
		return "", fmt.Errorf("rename downloaded file: file is outside the managed workspace")
	}
	fileInfo, err := os.Lstat(matchingFile.Path)
	if err != nil {
		return "", fmt.Errorf("inspect downloaded file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return "", fmt.Errorf("rename downloaded file: source must be a regular file")
	}
	newPath := filepath.Join(filepath.Dir(matchingFile.Path), newFilename)
	if !tool.contains(newPath) {
		return "", fmt.Errorf("rename downloaded file: destination is outside the managed workspace")
	}
	if _, err := os.Lstat(newPath); err == nil {
		return "", fmt.Errorf("rename downloaded file: destination already exists")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect rename destination: %w", err)
	}
	if err := os.Rename(matchingFile.Path, newPath); err != nil {
		return "", fmt.Errorf("rename downloaded file: %w", err)
	}
	if err := toolExecutionContext.RenameDownloadedFile(filename, newFilename, newPath); err != nil {
		return "", fmt.Errorf("rename downloaded file: %w", err)
	}
	result, err := json.Marshal(struct {
		Command          string `json:"command"`
		PreviousFilename string `json:"previous_filename"`
		Filename         string `json:"filename"`
		Status           string `json:"status"`
	}{
		Command:          renameFileCommand,
		PreviousFilename: filename,
		Filename:         newFilename,
		Status:           "renamed",
	})
	if err != nil {
		return "", fmt.Errorf("encode filesystem result: %w", err)
	}
	return string(result), nil
}

func workspaceRoot(rawRoot string) (string, error) {
	root := strings.TrimSpace(rawRoot)
	if root == "" {
		return "", fmt.Errorf("filesystem workspace root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve filesystem workspace root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return "", fmt.Errorf("create filesystem workspace root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve filesystem workspace symlinks: %w", err)
	}
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("inspect filesystem workspace root: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("filesystem workspace root must be a directory")
	}
	return filepath.Clean(canonicalRoot), nil
}

func (tool *Tool) contains(rawPath string) bool {
	cleanedPath := filepath.Clean(strings.TrimSpace(rawPath))
	relativePath, err := filepath.Rel(tool.root, cleanedPath)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}

func queuedDownloadedFile(queuedFiles []tools.DownloadedFile, filename string) (tools.DownloadedFile, bool) {
	var matchingFile tools.DownloadedFile
	for _, queuedFile := range queuedFiles {
		if queuedFile.Filename != filename {
			continue
		}
		if matchingFile.Path != "" {
			return tools.DownloadedFile{}, false
		}
		matchingFile = queuedFile
	}
	return matchingFile, matchingFile.Path != ""
}

func safeFilename(rawFilename string, fieldName string) (string, error) {
	filename := strings.TrimSpace(rawFilename)
	if filename == "" {
		return "", fmt.Errorf("%s must not be empty", fieldName)
	}
	if !utf8.ValidString(filename) {
		return "", fmt.Errorf("%s must be valid UTF-8", fieldName)
	}
	if utf8.RuneCountInString(filename) > maximumFilenameRunes {
		return "", fmt.Errorf("%s must not exceed %d characters", fieldName, maximumFilenameRunes)
	}
	if filename == "." || filename == ".." || filepath.Base(filename) != filename || strings.ContainsAny(filename, `\\/`) {
		return "", fmt.Errorf("%s must be a filename, not a path", fieldName)
	}
	if strings.IndexFunc(filename, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s must not contain control characters", fieldName)
	}
	return filename, nil
}
