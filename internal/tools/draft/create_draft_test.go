package draft

import (
	"context"
	"encoding/json"
	"testing"

	"tourplannerbot/internal/models"
	applicationTools "tourplannerbot/internal/tools"
)

// TestCreateDraftDefinitionDoesNotExposeTrustedIdentifiers verifies the model
// cannot select a draft owner or conversation through the function schema.
func TestCreateDraftDefinitionDoesNotExposeTrustedIdentifiers(t *testing.T) {
	tool := &Tool{}
	definition := tool.Definition()
	properties, propertiesAreMap := definition.Parameters["properties"].(map[string]any)
	if !propertiesAreMap {
		t.Fatal("create_draft properties are not an object")
	}
	if len(properties) != 2 || properties["kind"] == nil || properties["body"] == nil {
		t.Errorf("create_draft properties = %#v, want only kind and body", properties)
	}
	for _, forbiddenProperty := range []string{"chat_id", "message_thread_id", "owner_telegram_id", "revision", "status", "subject"} {
		if _, found := properties[forbiddenProperty]; found {
			t.Errorf("create_draft exposes forbidden property %q", forbiddenProperty)
		}
	}
	if !definition.Strict {
		t.Error("create_draft must use a strict function schema")
	}
	requiredProperties, requiredAreList := definition.Parameters["required"].([]string)
	if !requiredAreList || len(requiredProperties) != len(properties) {
		t.Errorf("update_draft required properties = %#v, want every property", definition.Parameters["required"])
	}
}

// TestCreateDraftRequiresTrustedContext verifies a direct registry invocation
// without an incoming Telegram identity cannot create a draft.
func TestCreateDraftRequiresTrustedContext(t *testing.T) {
	tool := &Tool{}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"kind":"email","body":"Hola"}`))
	if err == nil {
		t.Fatal("Execute accepted an invocation without trusted execution context")
	}
}

// TestUpdateDraftDefinitionDoesNotExposeDraftIdentity verifies the model can
// only update the active draft already supplied by the trusted handler context.
func TestUpdateDraftDefinitionDoesNotExposeDraftIdentity(t *testing.T) {
	tool := &UpdateTool{}
	definition := tool.Definition()
	properties := definition.Parameters["properties"].(map[string]any)
	if len(properties) != 2 || properties["body"] == nil || properties["expected_revision"] == nil {
		t.Errorf("update_draft properties = %#v, want body and expected_revision", properties)
	}
	for _, forbiddenProperty := range []string{"id", "draft_id", "chat_id", "message_thread_id", "owner_telegram_id", "kind", "status", "subject"} {
		if _, found := properties[forbiddenProperty]; found {
			t.Errorf("update_draft exposes forbidden property %q", forbiddenProperty)
		}
	}
	if !definition.Strict {
		t.Error("update_draft must use a strict function schema")
	}
	requiredProperties, requiredAreList := definition.Parameters["required"].([]string)
	if !requiredAreList || len(requiredProperties) != len(properties) {
		t.Errorf("update_draft required properties = %#v, want every property", definition.Parameters["required"])
	}
}

// TestUpdateDraftRequiresAnActiveDraft verifies a model cannot update a draft
// merely by guessing an identifier or revision.
func TestUpdateDraftRequiresAnActiveDraft(t *testing.T) {
	toolExecutionContext, err := applicationTools.NewExecutionContext(123, 0, 42)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	tool := &UpdateTool{}
	_, err = tool.Execute(applicationTools.WithExecutionContext(context.Background(), toolExecutionContext), json.RawMessage(`{"body":"Hola","expected_revision":1}`))
	if err == nil {
		t.Fatal("Execute accepted an update without an active draft")
	}
}

// TestCreateDraftRejectsASecondDraftInTheSameResponse verifies one model turn
// cannot accidentally supersede its own first proposal with another tool call.
func TestCreateDraftRejectsASecondDraftInTheSameResponse(t *testing.T) {
	toolExecutionContext, err := applicationTools.NewExecutionContext(123, 0, 42)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	toolExecutionContext.RecordCreatedDraft(&models.Draft{ID: "existing-draft"})
	tool := &Tool{}
	_, err = tool.Execute(applicationTools.WithExecutionContext(context.Background(), toolExecutionContext), json.RawMessage(`{"kind":"email","body":"Hola"}`))
	if err == nil {
		t.Fatal("Execute accepted a second draft in the same response")
	}
}
