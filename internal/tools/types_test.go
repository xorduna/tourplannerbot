package tools

import (
	"context"
	"testing"

	"tourplannerbot/internal/models"
)

// TestExecutionContextKeepsTrustedConversationValues verifies model arguments
// are not needed to make the source conversation available to a native tool.
func TestExecutionContextKeepsTrustedConversationValues(t *testing.T) {
	toolExecutionContext, err := NewExecutionContext(-100123, 17, 42)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	contextWithExecutionData := WithExecutionContext(context.Background(), toolExecutionContext)
	retrievedExecutionContext, found := ExecutionContextFromContext(contextWithExecutionData)
	if !found {
		t.Fatal("ExecutionContextFromContext did not find the trusted context")
	}
	if retrievedExecutionContext.ChatID != -100123 || retrievedExecutionContext.MessageThreadID != 17 || retrievedExecutionContext.OwnerTelegramID != 42 {
		t.Errorf("trusted execution context = %#v", retrievedExecutionContext)
	}
}

// TestExecutionContextRetainsActiveAndUpdatedDrafts verifies a native update
// tool can use the handler-loaded draft and report its canonical result back.
func TestExecutionContextRetainsActiveAndUpdatedDrafts(t *testing.T) {
	toolExecutionContext, err := NewExecutionContext(123, 0, 42)
	if err != nil {
		t.Fatalf("NewExecutionContext returned an error: %v", err)
	}
	activeDraft := &models.Draft{ID: "active", Revision: 3}
	updatedDraft := &models.Draft{ID: "active", Revision: 4}
	toolExecutionContext.SetActiveDraft(activeDraft)
	toolExecutionContext.RecordUpdatedDraft(updatedDraft)
	if toolExecutionContext.ActiveDraft() != activeDraft || toolExecutionContext.UpdatedDraft() != updatedDraft {
		t.Errorf("execution context did not retain active and updated drafts")
	}
}

// TestNewExecutionContextRejectsMissingTrustedIdentifiers verifies native tools
// cannot silently execute without a real Telegram conversation and user.
func TestNewExecutionContextRejectsMissingTrustedIdentifiers(t *testing.T) {
	if _, err := NewExecutionContext(0, 0, 42); err == nil {
		t.Error("NewExecutionContext accepted an empty chat ID")
	}
	if _, err := NewExecutionContext(123, 0, 0); err == nil {
		t.Error("NewExecutionContext accepted an empty owner Telegram ID")
	}
}
