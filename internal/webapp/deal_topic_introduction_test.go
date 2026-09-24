package webapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tourplannerbot/internal/llm"
	applicationTools "tourplannerbot/internal/tools"
)

type introductionResponseGeneratorStub struct {
	generation           llm.Generation
	instructions         string
	conversationMessages []llm.Message
	toolDefinitionCount  int
}

// Generate records the introduction request and returns the configured text.
func (responseGenerator *introductionResponseGeneratorStub) Generate(_ context.Context, instructions string, conversationMessages []llm.Message, toolDefinitions []applicationTools.Definition) (llm.Generation, error) {
	responseGenerator.instructions = instructions
	responseGenerator.conversationMessages = conversationMessages
	responseGenerator.toolDefinitionCount = len(toolDefinitions)
	return responseGenerator.generation, nil
}

func TestLLMDealTopicIntroductionUsesTrustedBiginDataWithoutTools(t *testing.T) {
	responseGenerator := &introductionResponseGeneratorStub{generation: llm.Generation{Text: "  👋 Resum inicial del tour  "}}
	introductionGenerator, err := NewLLMDealTopicIntroductionGenerator(responseGenerator)
	if err != nil {
		t.Fatalf("NewLLMDealTopicIntroductionGenerator returned error: %v", err)
	}

	introduction, err := introductionGenerator.GenerateIntroduction(context.Background(), "2034020000000489080", json.RawMessage(`{"data":[{"Deal_Name":"Barcelona visit","Stage":"Qualification"}]}`))
	if err != nil {
		t.Fatalf("GenerateIntroduction returned error: %v", err)
	}
	if introduction != "👋 Resum inicial del tour" {
		t.Errorf("introduction = %q", introduction)
	}
	if !strings.Contains(responseGenerator.instructions, "Write in Catalan") {
		t.Errorf("instructions do not require Catalan: %s", responseGenerator.instructions)
	}
	if responseGenerator.toolDefinitionCount != 0 {
		t.Errorf("tool definition count = %d, want 0", responseGenerator.toolDefinitionCount)
	}
	if len(responseGenerator.conversationMessages) != 1 || !strings.Contains(responseGenerator.conversationMessages[0].Content, `"deal_id":"2034020000000489080"`) || !strings.Contains(responseGenerator.conversationMessages[0].Content, `"Deal_Name":"Barcelona visit"`) {
		t.Errorf("conversation messages = %#v", responseGenerator.conversationMessages)
	}
}

func TestLLMDealTopicIntroductionRejectsInvalidBiginJSON(t *testing.T) {
	introductionGenerator, err := NewLLMDealTopicIntroductionGenerator(&introductionResponseGeneratorStub{})
	if err != nil {
		t.Fatalf("NewLLMDealTopicIntroductionGenerator returned error: %v", err)
	}
	if _, err := introductionGenerator.GenerateIntroduction(context.Background(), "123", json.RawMessage(`not-json`)); err == nil {
		t.Fatal("GenerateIntroduction returned nil error for invalid Bigin JSON")
	}
}
