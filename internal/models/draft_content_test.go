package models

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestNewTiptapDocumentFromPlainTextPreservesParagraphs verifies the initial
// draft document uses only the small, supported Tiptap node set.
func TestNewTiptapDocumentFromPlainTextPreservesParagraphs(t *testing.T) {
	contentJSON, err := NewTiptapDocumentFromPlainText("Bon dia\n\nFins aviat")
	if err != nil {
		t.Fatalf("NewTiptapDocumentFromPlainText returned error: %v", err)
	}
	var document tiptapDocument
	if err := json.Unmarshal(contentJSON, &document); err != nil {
		t.Fatalf("unmarshal generated document: %v", err)
	}
	if document.Type != "doc" || len(document.Content) != 3 {
		t.Fatalf("document = %#v, want a document with three paragraphs", document)
	}
	if document.Content[0].Type != "paragraph" || len(document.Content[0].Content) != 1 || document.Content[0].Content[0].Text != "Bon dia" {
		t.Errorf("first paragraph = %#v, want Bon dia text paragraph", document.Content[0])
	}
	if document.Content[1].Type != "paragraph" || len(document.Content[1].Content) != 0 {
		t.Errorf("second paragraph = %#v, want an empty paragraph", document.Content[1])
	}
	if document.Content[2].Type != "paragraph" || len(document.Content[2].Content) != 1 || document.Content[2].Content[0].Text != "Fins aviat" {
		t.Errorf("third paragraph = %#v, want Fins aviat text paragraph", document.Content[2])
	}
}

// TestValidateAndProjectTiptapDocumentAcceptsOnlySupportedNodes verifies the
// server derives text itself and rejects nodes or marks the editor does not allow.
func TestValidateAndProjectTiptapDocumentAcceptsOnlySupportedNodes(t *testing.T) {
	testCases := []struct {
		name      string
		content   string
		wantText  string
		wantError bool
	}{
		{
			name:     "paragraph with supported marks and hard break",
			content:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Bon","marks":[{"type":"bold"}]},{"type":"hardBreak"},{"type":"text","text":"dia","marks":[{"type":"italic"}]}]}]}`,
			wantText: "Bon\ndia",
		},
		{
			name:     "ordered list",
			content:  `{"type":"doc","content":[{"type":"orderedList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Primer"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Segon"}]}]}]}]}`,
			wantText: "1. Primer\n2. Segon",
		},
		{
			name:      "disallowed heading",
			content:   `{"type":"doc","content":[{"type":"heading","content":[{"type":"text","text":"No"}]}]}`,
			wantError: true,
		},
		{
			name:     "supported HTTPS link mark",
			content:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"No","marks":[{"type":"link","attrs":{"href":"https://example.com","target":"_blank","rel":"noopener noreferrer nofollow","class":null,"title":null}}]}]}]}`,
			wantText: "No",
		},
		{
			name:      "disallowed unsafe link mark",
			content:   `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"No","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}`,
			wantError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			bodyText, err := ValidateAndProjectTiptapDocument(json.RawMessage(testCase.content))
			if testCase.wantError {
				if !errors.Is(err, ErrInvalidDraftContent) {
					t.Errorf("ValidateAndProjectTiptapDocument error = %v, want ErrInvalidDraftContent", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateAndProjectTiptapDocument returned error: %v", err)
			}
			if bodyText != testCase.wantText {
				t.Errorf("body text = %q, want %q", bodyText, testCase.wantText)
			}
		})
	}
}

// TestMarkdownDraftContentRoundTripsSupportedFormatting verifies agent-created
// Markdown becomes editable Tiptap content and can be faithfully previewed.
func TestMarkdownDraftContentRoundTripsSupportedFormatting(t *testing.T) {
	input := "Hola **Diana**,\n\n- Primer *punt*\n- [Més informació](https://example.com/info)\n\n1. Un\n2. Dos"
	contentJSON, err := NewTiptapDocumentFromMarkdown(input)
	if err != nil {
		t.Fatalf("NewTiptapDocumentFromMarkdown returned error: %v", err)
	}
	bodyText, err := ValidateAndProjectTiptapDocument(contentJSON)
	if err != nil {
		t.Fatalf("ValidateAndProjectTiptapDocument returned error: %v", err)
	}
	if bodyText != "Hola Diana,\n\n- Primer punt\n- Més informació\n\n1. Un\n2. Dos" {
		t.Errorf("body text = %q", bodyText)
	}
	markdown, err := TiptapDocumentToMarkdown(contentJSON)
	if err != nil {
		t.Fatalf("TiptapDocumentToMarkdown returned error: %v", err)
	}
	if markdown != input {
		t.Errorf("Markdown preview = %q, want %q", markdown, input)
	}
}
