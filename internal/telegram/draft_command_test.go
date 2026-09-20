package telegram

import (
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

// TestDraftBodyPreviewDoesNotSplitUnicodeCharacters verifies command feedback
// can truncate a long Unicode draft without corrupting its displayed text.
func TestDraftBodyPreviewDoesNotSplitUnicodeCharacters(t *testing.T) {
	bodyText := string(make([]rune, 0, 501))
	for characterIndex := 0; characterIndex < 501; characterIndex++ {
		bodyText += "à"
	}
	preview := draftBodyPreview(bodyText)
	if len([]rune(preview)) != 501 || []rune(preview)[500] != '…' {
		t.Errorf("draftBodyPreview rune length/end = %d/%q, want 501/ellipsis", len([]rune(preview)), []rune(preview)[len([]rune(preview))-1])
	}
}
