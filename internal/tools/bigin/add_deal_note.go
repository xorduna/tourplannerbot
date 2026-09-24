package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	addDealNoteToolName  = "add_bigin_deal_note"
	maximumNoteTitleSize = 255
	maximumNoteBodyBytes = 1 << 20
)

// AddDealNoteTool creates a note associated with one Bigin pipeline record.
type AddDealNoteTool struct {
	client *Client
}

// NewAddDealNote creates the Bigin deal-note creation tool.
func NewAddDealNote(client *Client) (*AddDealNoteTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	return &AddDealNoteTool{client: client}, nil
}

// Definition describes add_bigin_deal_note to the LLM.
func (addDealNoteTool *AddDealNoteTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        addDealNoteToolName,
		Description: "Add a note to a Zoho Bigin deal (pipeline record). Use this only when the user explicitly asks to save or add a note to the deal. An empty title creates an untitled note.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID supplied by the user.",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Optional note title. Pass an empty string to create an untitled note.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The complete content to save in the note.",
				},
			},
			"required":             []string{"deal_id", "title", "content"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute validates the note and creates it through the record-specific Bigin
// notes endpoint, returning Bigin's complete operation response.
func (addDealNoteTool *AddDealNoteTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID  string `json:"deal_id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", addDealNoteToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", addDealNoteToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", addDealNoteToolName, err)
	}

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	title := strings.TrimSpace(arguments.Title)
	if !utf8.ValidString(title) {
		return "", fmt.Errorf("title must be valid UTF-8")
	}
	if utf8.RuneCountInString(title) > maximumNoteTitleSize {
		return "", fmt.Errorf("title must not exceed %d characters", maximumNoteTitleSize)
	}
	content := arguments.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("content must not be empty")
	}
	if !utf8.ValidString(content) {
		return "", fmt.Errorf("content must be valid UTF-8")
	}
	if len(content) > maximumNoteBodyBytes {
		return "", fmt.Errorf("content must not exceed %d bytes", maximumNoteBodyBytes)
	}

	note := struct {
		Title   string `json:"Note_Title,omitempty"`
		Content string `json:"Note_Content"`
	}{
		Title:   title,
		Content: content,
	}
	requestBody := struct {
		Data []any `json:"data"`
	}{Data: []any{note}}
	responseBody, err := addDealNoteTool.client.postJSON(ctx, "/bigin/v2/Pipelines/"+dealID+"/Notes", requestBody)
	if err != nil {
		return "", fmt.Errorf("add Bigin deal note: %w", err)
	}
	return string(responseBody), nil
}
