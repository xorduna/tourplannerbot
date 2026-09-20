package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaximumDraftDocumentBytes limits the serialized Tiptap document accepted by the editor API.
const MaximumDraftDocumentBytes = 50 * 1024

// ErrInvalidDraftContent indicates content outside the supported Tiptap schema.
var ErrInvalidDraftContent = errors.New("invalid draft content")

type tiptapDocument struct {
	Type    string            `json:"type"`
	Content []tiptapParagraph `json:"content"`
}

type tiptapParagraph struct {
	Type    string       `json:"type"`
	Content []tiptapText `json:"content,omitempty"`
}

type tiptapText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewTiptapDocumentFromPlainText converts plain text into the deliberately
// minimal document format used for a newly created draft. Each line becomes a
// paragraph, preserving blank lines as empty paragraphs for the editor.
func NewTiptapDocumentFromPlainText(bodyText string) (json.RawMessage, error) {
	paragraphTexts := strings.Split(bodyText, "\n")
	document := tiptapDocument{
		Type:    "doc",
		Content: make([]tiptapParagraph, 0, len(paragraphTexts)),
	}
	for _, paragraphText := range paragraphTexts {
		paragraph := tiptapParagraph{Type: "paragraph"}
		if paragraphText != "" {
			paragraph.Content = []tiptapText{{Type: "text", Text: paragraphText}}
		}
		document.Content = append(document.Content, paragraph)
	}
	return json.Marshal(document)
}

// ValidateAndProjectTiptapDocument accepts only the minimal document schema
// supported by the draft editor and derives its canonical plain-text projection.
func ValidateAndProjectTiptapDocument(contentJSON json.RawMessage) (string, error) {
	if len(contentJSON) == 0 || len(contentJSON) > MaximumDraftDocumentBytes {
		return "", ErrInvalidDraftContent
	}
	decoder := json.NewDecoder(bytes.NewReader(contentJSON))
	decoder.UseNumber()
	var documentValue any
	if err := decoder.Decode(&documentValue); err != nil {
		return "", fmt.Errorf("%w: decode JSON", ErrInvalidDraftContent)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: multiple JSON values", ErrInvalidDraftContent)
	}
	document, isDocument := documentValue.(map[string]any)
	if !isDocument || document["type"] != "doc" || !hasOnlyKeys(document, "type", "content") {
		return "", fmt.Errorf("%w: expected document node", ErrInvalidDraftContent)
	}
	content, isContent := document["content"].([]any)
	if !isContent {
		return "", fmt.Errorf("%w: document content", ErrInvalidDraftContent)
	}

	blockTexts := make([]string, 0, len(content))
	for _, blockValue := range content {
		blockText, err := projectDraftBlock(blockValue)
		if err != nil {
			return "", err
		}
		blockTexts = append(blockTexts, blockText)
	}
	return strings.Join(blockTexts, "\n"), nil
}

// projectDraftBlock validates and projects one block-level supported node.
func projectDraftBlock(blockValue any) (string, error) {
	block, isBlock := blockValue.(map[string]any)
	if !isBlock {
		return "", fmt.Errorf("%w: block node", ErrInvalidDraftContent)
	}
	switch blockType := block["type"]; blockType {
	case "paragraph":
		if !hasOnlyKeys(block, "type", "content") {
			return "", fmt.Errorf("%w: paragraph fields", ErrInvalidDraftContent)
		}
		return projectDraftInlineContent(block["content"])
	case "bulletList", "orderedList":
		if !hasOnlyKeys(block, "type", "content") {
			return "", fmt.Errorf("%w: list fields", ErrInvalidDraftContent)
		}
		listItems, isListItems := block["content"].([]any)
		if !isListItems || len(listItems) == 0 {
			return "", fmt.Errorf("%w: list content", ErrInvalidDraftContent)
		}
		projectedItems := make([]string, 0, len(listItems))
		for itemIndex, itemValue := range listItems {
			itemText, err := projectDraftListItem(itemValue)
			if err != nil {
				return "", err
			}
			prefix := "- "
			if blockType == "orderedList" {
				prefix = fmt.Sprintf("%d. ", itemIndex+1)
			}
			projectedItems = append(projectedItems, prefix+itemText)
		}
		return strings.Join(projectedItems, "\n"), nil
	default:
		return "", fmt.Errorf("%w: unsupported node %q", ErrInvalidDraftContent, blockType)
	}
}

// projectDraftListItem validates a flat list item composed of one or more paragraphs.
func projectDraftListItem(itemValue any) (string, error) {
	item, isItem := itemValue.(map[string]any)
	if !isItem || item["type"] != "listItem" || !hasOnlyKeys(item, "type", "content") {
		return "", fmt.Errorf("%w: list item", ErrInvalidDraftContent)
	}
	paragraphs, isParagraphs := item["content"].([]any)
	if !isParagraphs || len(paragraphs) == 0 {
		return "", fmt.Errorf("%w: list item content", ErrInvalidDraftContent)
	}
	projectedParagraphs := make([]string, 0, len(paragraphs))
	for _, paragraphValue := range paragraphs {
		paragraph, isParagraph := paragraphValue.(map[string]any)
		if !isParagraph || paragraph["type"] != "paragraph" || !hasOnlyKeys(paragraph, "type", "content") {
			return "", fmt.Errorf("%w: list item paragraph", ErrInvalidDraftContent)
		}
		paragraphText, err := projectDraftInlineContent(paragraph["content"])
		if err != nil {
			return "", err
		}
		projectedParagraphs = append(projectedParagraphs, paragraphText)
	}
	return strings.Join(projectedParagraphs, "\n"), nil
}

// projectDraftInlineContent validates text, bold/italic marks, and hard breaks.
func projectDraftInlineContent(contentValue any) (string, error) {
	if contentValue == nil {
		return "", nil
	}
	inlineNodes, isInlineNodes := contentValue.([]any)
	if !isInlineNodes {
		return "", fmt.Errorf("%w: inline content", ErrInvalidDraftContent)
	}
	var projectedText strings.Builder
	for _, inlineNodeValue := range inlineNodes {
		inlineNode, isInlineNode := inlineNodeValue.(map[string]any)
		if !isInlineNode {
			return "", fmt.Errorf("%w: inline node", ErrInvalidDraftContent)
		}
		switch inlineType := inlineNode["type"]; inlineType {
		case "text":
			if !hasOnlyKeys(inlineNode, "type", "text", "marks") {
				return "", fmt.Errorf("%w: text fields", ErrInvalidDraftContent)
			}
			text, isText := inlineNode["text"].(string)
			if !isText || text == "" || !validDraftMarks(inlineNode["marks"]) {
				return "", fmt.Errorf("%w: text node", ErrInvalidDraftContent)
			}
			projectedText.WriteString(text)
		case "hardBreak":
			if !hasOnlyKeys(inlineNode, "type") {
				return "", fmt.Errorf("%w: hard break fields", ErrInvalidDraftContent)
			}
			projectedText.WriteByte('\n')
		default:
			return "", fmt.Errorf("%w: unsupported inline node %q", ErrInvalidDraftContent, inlineType)
		}
	}
	return projectedText.String(), nil
}

// validDraftMarks accepts only the editor's supported bold and italic marks.
func validDraftMarks(marksValue any) bool {
	if marksValue == nil {
		return true
	}
	marks, isMarks := marksValue.([]any)
	if !isMarks {
		return false
	}
	for _, markValue := range marks {
		mark, isMark := markValue.(map[string]any)
		if !isMark || !hasOnlyKeys(mark, "type") || (mark["type"] != "bold" && mark["type"] != "italic") {
			return false
		}
	}
	return true
}

// hasOnlyKeys reports whether object contains no fields outside allowedKeys.
func hasOnlyKeys(object map[string]any, allowedKeys ...string) bool {
	allowedKeySet := make(map[string]struct{}, len(allowedKeys))
	for _, allowedKey := range allowedKeys {
		allowedKeySet[allowedKey] = struct{}{}
	}
	for objectKey := range object {
		if _, isAllowed := allowedKeySet[objectKey]; !isAllowed {
			return false
		}
	}
	return true
}
