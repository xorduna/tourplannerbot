package telegram

import (
	"strings"
	"testing"

	"tourplannerbot/internal/models"
)

// TestParseDraftCommandAcceptsTemporaryDraftOperations verifies the command
// parser preserves user text while rejecting unsupported operations before the
// message can reach the model.
func TestParseDraftCommandAcceptsTemporaryDraftOperations(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		wantCommand bool
		wantAction  string
		wantKind    models.DraftKind
		wantBody    string
	}{
		{name: "create WhatsApp draft", input: "/draft create whatsapp Bon dia, com estàs?", wantCommand: true, wantAction: "create", wantKind: models.DraftKindWhatsApp, wantBody: "Bon dia, com estàs?"},
		{name: "create command addressed to bot", input: "/draft@tourplannerbot create email Hola\nGràcies", wantCommand: true, wantAction: "create", wantKind: models.DraftKindEmail, wantBody: "Hola\nGràcies"},
		{name: "active draft", input: "/draft active", wantCommand: true, wantAction: "active"},
		{name: "invalid kind", input: "/draft create sms Hola", wantCommand: true},
		{name: "missing body", input: "/draft create generic", wantCommand: true},
		{name: "normal message", input: "Prepara'm un correu", wantCommand: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsedCommand, isDraftCommand := parseDraftCommand(testCase.input)
			if isDraftCommand != testCase.wantCommand {
				t.Fatalf("parseDraftCommand command match = %t, want %t", isDraftCommand, testCase.wantCommand)
			}
			if parsedCommand.action != testCase.wantAction || parsedCommand.kind != testCase.wantKind || parsedCommand.body != testCase.wantBody {
				t.Errorf("parseDraftCommand = %#v, want action=%q kind=%q body=%q", parsedCommand, testCase.wantAction, testCase.wantKind, testCase.wantBody)
			}
		})
	}
}

// TestDraftPreviewTextShowsTheCompleteUnicodeDraft verifies the Telegram
// preview no longer truncates a proposal before its Edit button.
func TestDraftPreviewTextShowsTheCompleteUnicodeDraft(t *testing.T) {
	bodyText := string(make([]rune, 0, 501))
	for characterIndex := 0; characterIndex < 501; characterIndex++ {
		bodyText += "à"
	}
	contentJSON, err := models.NewTiptapDocumentFromPlainText(bodyText)
	if err != nil {
		t.Fatalf("NewTiptapDocumentFromPlainText returned error: %v", err)
	}
	preview := draftPreviewText("T’he preparat aquesta proposta.", &models.Draft{ContentJSON: contentJSON, BodyText: bodyText})
	if !strings.Contains(preview, bodyText) || strings.Contains(preview, "…") {
		t.Errorf("draft preview did not preserve complete Unicode body: %q", preview)
	}
}

// TestActiveDraftContextMessageContainsCanonicalCurrentVersion verifies the
// agent sees the persisted revision and body but the wrapper itself is merely
// an in-memory LLM message.
func TestActiveDraftContextMessageContainsCanonicalCurrentVersion(t *testing.T) {
	contentJSON, err := models.NewTiptapDocumentFromMarkdown("Hola **Diana**")
	if err != nil {
		t.Fatalf("NewTiptapDocumentFromMarkdown returned error: %v", err)
	}
	contextMessage := activeDraftContextMessage(&models.Draft{
		ID:          "2ee30369-f4ae-4741-8cfc-e58f2eb9b5f1",
		Kind:        models.DraftKindEmail,
		ContentJSON: contentJSON,
		BodyText:    "Hola Diana",
		Revision:    4,
	})
	if contextMessage.Role != models.MessageRoleUser || !strings.Contains(contextMessage.Content, `"revision":4`) || !strings.Contains(contextMessage.Content, `"body_markdown":"Hola **Diana**"`) {
		t.Errorf("active draft context = %#v", contextMessage)
	}
}
