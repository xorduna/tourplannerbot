package models

import (
	"encoding/json"
	"strings"
)

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
