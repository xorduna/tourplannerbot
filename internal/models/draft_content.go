package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
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
	Type  string       `json:"type"`
	Text  string       `json:"text"`
	Marks []tiptapMark `json:"marks,omitempty"`
}

type tiptapMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

var orderedMarkdownListItemPattern = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)

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

// NewTiptapDocumentFromMarkdown converts the small Markdown subset used by
// agent-created drafts into the editor's canonical rich document. It supports
// paragraphs, unordered and ordered lists, bold, italic, and HTTPS/HTTP links.
func NewTiptapDocumentFromMarkdown(markdown string) (json.RawMessage, error) {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n"), "\n")
	content := make([]any, 0, len(lines))
	for lineIndex := 0; lineIndex < len(lines); {
		if listType, _, isListItem := markdownListItem(lines[lineIndex]); isListItem {
			listItems := make([]any, 0)
			for lineIndex < len(lines) {
				candidateListType, candidateText, candidateIsListItem := markdownListItem(lines[lineIndex])
				if !candidateIsListItem || candidateListType != listType {
					break
				}
				inlineContent, err := markdownInlineContent(candidateText)
				if err != nil {
					return nil, err
				}
				listItems = append(listItems, map[string]any{
					"type":    "listItem",
					"content": []any{map[string]any{"type": "paragraph", "content": inlineContent}},
				})
				lineIndex++
			}
			content = append(content, map[string]any{"type": listType, "content": listItems})
			continue
		}

		inlineContent, err := markdownInlineContent(lines[lineIndex])
		if err != nil {
			return nil, err
		}
		paragraph := map[string]any{"type": "paragraph"}
		if len(inlineContent) > 0 {
			paragraph["content"] = inlineContent
		}
		content = append(content, paragraph)
		lineIndex++
	}
	return json.Marshal(map[string]any{"type": "doc", "content": content})
}

// markdownListItem identifies the intentionally flat list syntax accepted for
// a newly generated draft and returns its matching Tiptap list node type.
func markdownListItem(line string) (string, string, bool) {
	trimmedLine := strings.TrimSpace(line)
	if len(trimmedLine) > 2 && (strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(trimmedLine, "* ") || strings.HasPrefix(trimmedLine, "+ ")) {
		return "bulletList", strings.TrimSpace(trimmedLine[2:]), true
	}
	matches := orderedMarkdownListItemPattern.FindStringSubmatch(trimmedLine)
	if matches != nil {
		return "orderedList", matches[2], true
	}
	return "", "", false
}

// markdownInlineContent parses the minimal inline syntax agent prompts use and
// leaves unmatched markers as ordinary text rather than silently dropping it.
func markdownInlineContent(markdown string) ([]tiptapText, error) {
	content := make([]tiptapText, 0)
	for position := 0; position < len(markdown); {
		if strings.HasPrefix(markdown[position:], "[") {
			if labelEnd := strings.Index(markdown[position:], "]("); labelEnd > 1 {
				absoluteLabelEnd := position + labelEnd
				if urlEnd := strings.Index(markdown[absoluteLabelEnd+2:], ")"); urlEnd >= 0 {
					absoluteURLEnd := absoluteLabelEnd + 2 + urlEnd
					linkURL := markdown[absoluteLabelEnd+2 : absoluteURLEnd]
					if validDraftLinkURL(linkURL) {
						linkText, err := markdownInlineContent(markdown[position+1 : absoluteLabelEnd])
						if err != nil {
							return nil, err
						}
						content = append(content, addMarkdownMark(linkText, tiptapMark{Type: "link", Attrs: map[string]any{"href": linkURL}})...)
						position = absoluteURLEnd + 1
						continue
					}
				}
			}
		}
		if marker, markType := markdownMarkAt(markdown[position:]); marker != "" {
			contentStart := position + len(marker)
			if contentEnd := strings.Index(markdown[contentStart:], marker); contentEnd > 0 {
				absoluteContentEnd := contentStart + contentEnd
				markedContent, err := markdownInlineContent(markdown[contentStart:absoluteContentEnd])
				if err != nil {
					return nil, err
				}
				content = append(content, addMarkdownMark(markedContent, tiptapMark{Type: markType})...)
				position = absoluteContentEnd + len(marker)
				continue
			}
		}

		nextPosition := len(markdown)
		for _, marker := range []string{"[", "**", "__", "*", "_"} {
			if markerPosition := strings.Index(markdown[position+1:], marker); markerPosition >= 0 && position+1+markerPosition < nextPosition {
				nextPosition = position + 1 + markerPosition
			}
		}
		content = append(content, tiptapText{Type: "text", Text: markdown[position:nextPosition]})
		position = nextPosition
	}
	return content, nil
}

// markdownMarkAt reports the supported Markdown emphasis marker at the start
// of text, preferring the two-character variants before single-character ones.
func markdownMarkAt(markdown string) (string, string) {
	switch {
	case strings.HasPrefix(markdown, "**"):
		return "**", "bold"
	case strings.HasPrefix(markdown, "__"):
		return "__", "bold"
	case strings.HasPrefix(markdown, "*"):
		return "*", "italic"
	case strings.HasPrefix(markdown, "_"):
		return "_", "italic"
	default:
		return "", ""
	}
}

// addMarkdownMark returns a copy of inline content with a new supported mark.
func addMarkdownMark(content []tiptapText, mark tiptapMark) []tiptapText {
	markedContent := make([]tiptapText, len(content))
	for textIndex, text := range content {
		text.Marks = append(append([]tiptapMark(nil), text.Marks...), mark)
		markedContent[textIndex] = text
	}
	return markedContent
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

// validDraftMarks accepts only the editor's supported bold, italic, and link marks.
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
		if !isMark {
			return false
		}
		switch mark["type"] {
		case "bold", "italic":
			if !hasOnlyKeys(mark, "type") {
				return false
			}
		case "link":
			if !hasOnlyKeys(mark, "type", "attrs") {
				return false
			}
			attributes, areAttributesValid := mark["attrs"].(map[string]any)
			if !areAttributesValid || !validDraftLinkAttributes(attributes) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// validDraftLinkAttributes accepts href plus the nullable attributes emitted by
// Tiptap's Link extension when an editor saves an otherwise unchanged link.
func validDraftLinkAttributes(attributes map[string]any) bool {
	if !hasOnlyKeys(attributes, "href", "target", "rel", "class", "title") {
		return false
	}
	href, isHrefValid := attributes["href"].(string)
	if !isHrefValid || !validDraftLinkURL(href) {
		return false
	}
	for _, attributeName := range []string{"target", "rel", "class", "title"} {
		attributeValue, found := attributes[attributeName]
		if !found || attributeValue == nil {
			continue
		}
		if _, isString := attributeValue.(string); !isString {
			return false
		}
	}
	return true
}

// validDraftLinkURL accepts the same externally navigable link schemes shown
// by Telegram previews and supported by the Mini App editor.
func validDraftLinkURL(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	return err == nil && (parsedURL.Scheme == "https" || parsedURL.Scheme == "http") && parsedURL.Host != ""
}

// TiptapDocumentToMarkdown reconstructs a safe Markdown representation of a
// validated draft for renderers that need its lists, links, bold, and italic
// formatting without accepting arbitrary HTML from stored content.
func TiptapDocumentToMarkdown(contentJSON json.RawMessage) (string, error) {
	if _, err := ValidateAndProjectTiptapDocument(contentJSON); err != nil {
		return "", err
	}
	var document map[string]any
	if err := json.Unmarshal(contentJSON, &document); err != nil {
		return "", fmt.Errorf("%w: decode Markdown projection", ErrInvalidDraftContent)
	}
	blocks := document["content"].([]any)
	markdownBlocks := make([]string, 0, len(blocks))
	for _, blockValue := range blocks {
		markdownBlock, err := tiptapBlockToMarkdown(blockValue)
		if err != nil {
			return "", err
		}
		markdownBlocks = append(markdownBlocks, markdownBlock)
	}
	return strings.Join(markdownBlocks, "\n"), nil
}

// tiptapBlockToMarkdown converts one already validated block to Markdown.
func tiptapBlockToMarkdown(blockValue any) (string, error) {
	block := blockValue.(map[string]any)
	switch block["type"] {
	case "paragraph":
		return tiptapInlineContentToMarkdown(block["content"])
	case "bulletList", "orderedList":
		listItems := block["content"].([]any)
		markdownItems := make([]string, 0, len(listItems))
		for itemIndex, itemValue := range listItems {
			item := itemValue.(map[string]any)
			paragraphs := item["content"].([]any)
			paragraphMarkdown := make([]string, 0, len(paragraphs))
			for _, paragraphValue := range paragraphs {
				paragraph := paragraphValue.(map[string]any)
				text, err := tiptapInlineContentToMarkdown(paragraph["content"])
				if err != nil {
					return "", err
				}
				paragraphMarkdown = append(paragraphMarkdown, text)
			}
			prefix := "- "
			if block["type"] == "orderedList" {
				prefix = fmt.Sprintf("%d. ", itemIndex+1)
			}
			markdownItems = append(markdownItems, prefix+strings.Join(paragraphMarkdown, "\n"))
		}
		return strings.Join(markdownItems, "\n"), nil
	default:
		return "", ErrInvalidDraftContent
	}
}

// tiptapInlineContentToMarkdown converts validated inline nodes while escaping
// literal Markdown punctuation so only the document's actual marks render.
func tiptapInlineContentToMarkdown(contentValue any) (string, error) {
	if contentValue == nil {
		return "", nil
	}
	inlineNodes := contentValue.([]any)
	var markdown strings.Builder
	for _, inlineNodeValue := range inlineNodes {
		inlineNode := inlineNodeValue.(map[string]any)
		if inlineNode["type"] == "hardBreak" {
			markdown.WriteByte('\n')
			continue
		}
		text := escapeLiteralMarkdown(inlineNode["text"].(string))
		if marksValue, hasMarks := inlineNode["marks"]; hasMarks {
			for _, markValue := range marksValue.([]any) {
				mark := markValue.(map[string]any)
				switch mark["type"] {
				case "bold":
					text = "**" + text + "**"
				case "italic":
					text = "*" + text + "*"
				case "link":
					href := mark["attrs"].(map[string]any)["href"].(string)
					text = "[" + text + "](" + href + ")"
				}
			}
		}
		markdown.WriteString(text)
	}
	return markdown.String(), nil
}

// escapeLiteralMarkdown prevents literal text that resembles Markdown from
// acquiring presentation formatting in a Telegram preview.
func escapeLiteralMarkdown(text string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)")
	return replacer.Replace(text)
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
