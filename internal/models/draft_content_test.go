package models

import (
	"encoding/json"
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
