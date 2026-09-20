package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	applicationModels "tourplannerbot/internal/models"

	"github.com/go-telegram/bot"
)

// recordedTelegramRequest contains the method name and decoded multipart fields
// from one test request sent through the Telegram client.
type recordedTelegramRequest struct {
	methodName string
	fields     map[string]string
}

// recordingTelegramHTTPClient records Telegram API calls and returns successful
// deterministic responses without making network requests.
type recordingTelegramHTTPClient struct {
	requestsMutex sync.Mutex
	requests      []recordedTelegramRequest
}

// Do implements bot.HttpClient and records the method and multipart parameters.
func (client *recordingTelegramHTTPClient) Do(request *http.Request) (*http.Response, error) {
	requestParseError := request.ParseMultipartForm(1 << 20)
	if requestParseError != nil {
		return nil, requestParseError
	}
	requestFields := make(map[string]string)
	if request.MultipartForm != nil {
		for fieldName, fieldValues := range request.MultipartForm.Value {
			if len(fieldValues) > 0 {
				requestFields[fieldName] = fieldValues[0]
			}
		}
	}
	methodName := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	client.requestsMutex.Lock()
	client.requests = append(client.requests, recordedTelegramRequest{methodName: methodName, fields: requestFields})
	client.requestsMutex.Unlock()

	responseBody := `{"ok":true,"result":true}`
	if methodName == "sendMessage" || methodName == "editMessageText" {
		responseBody = `{"ok":true,"result":{"message_id":77,"date":1,"chat":{"id":123,"type":"private"},"text":"ok"}}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Header:     make(http.Header),
	}, nil
}

// recordedRequests returns a copy of all captured calls.
func (client *recordingTelegramHTTPClient) recordedRequests() []recordedTelegramRequest {
	client.requestsMutex.Lock()
	defer client.requestsMutex.Unlock()
	return append([]recordedTelegramRequest(nil), client.requests...)
}

func TestToolProgressText(t *testing.T) {
	testCases := []struct {
		name         string
		toolName     string
		toolSource   string
		expectedText string
	}{
		{name: "current time", toolName: "current_time", toolSource: "native", expectedText: "🕐 Consultant l’hora…"},
		{name: "create draft", toolName: "create_draft", toolSource: "native", expectedText: "📝 Preparant la proposta…"},
		{name: "update draft", toolName: "update_draft", toolSource: "native", expectedText: "📝 Actualitzant la proposta…"},
		{name: "OpenStreetMap nearby", toolName: "query_nearby", toolSource: "openstreetmap", expectedText: "🗺️ Utilitzant OpenStreetMap per buscar llocs propers…"},
		{name: "Wikipedia search", toolName: "search_wikipedia", toolSource: "wikipedia", expectedText: "🔎 Utilitzant Wikipedia per buscar informació…"},
		{name: "unprefixed Wikipedia summary", toolName: "get_summary", expectedText: "🔎 Utilitzant Wikipedia per obtenir un resum…"},
		{name: "unknown tool", toolName: "weather_forecast", expectedText: "🛠️ Utilitzant weather_forecast…"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actualText := toolProgressText(testCase.toolName, testCase.toolSource)
			if actualText != testCase.expectedText {
				t.Fatalf("toolProgressText(%q) = %q, want %q", testCase.toolName, actualText, testCase.expectedText)
			}
		})
	}
}

func TestTelegramResponseProgressLifecycle(t *testing.T) {
	recordingHTTPClient := &recordingTelegramHTTPClient{}
	telegramBot, telegramBotError := bot.New(
		"test-token",
		bot.WithSkipGetMe(),
		bot.WithHTTPClient(time.Second, recordingHTTPClient),
	)
	if telegramBotError != nil {
		t.Fatalf("create Telegram bot: %v", telegramBotError)
	}
	handler := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	progress := newTelegramResponseProgress(context.Background(), handler, telegramBot, 123, 42)
	progress.reportToolUse(context.Background(), "query_nearby", "openstreetmap")
	progress.reportPreparingResponse(context.Background())
	progress.finish(context.Background(), "Resposta **final**")

	recordedRequests := recordingHTTPClient.recordedRequests()
	if len(recordedRequests) != 7 {
		t.Fatalf("recorded %d Telegram requests, want 7: %#v", len(recordedRequests), recordedRequests)
	}
	expectedMethods := []string{"sendMessage", "sendChatAction", "editMessageText", "sendChatAction", "editMessageText", "sendChatAction", "editMessageText"}
	for requestIndex, expectedMethod := range expectedMethods {
		if recordedRequests[requestIndex].methodName != expectedMethod {
			t.Fatalf("request %d method = %q, want %q", requestIndex, recordedRequests[requestIndex].methodName, expectedMethod)
		}
	}
	if recordedRequests[0].fields["text"] != "💭 Pensant…" {
		t.Fatalf("initial status = %q", recordedRequests[0].fields["text"])
	}
	if recordedRequests[1].fields["action"] != "typing" {
		t.Fatalf("chat action = %q", recordedRequests[1].fields["action"])
	}
	if recordedRequests[2].fields["text"] != "🗺️ Utilitzant OpenStreetMap per buscar llocs propers…" {
		t.Fatalf("tool status = %q", recordedRequests[2].fields["text"])
	}
	if recordedRequests[4].fields["text"] != "✍️ Preparant la resposta…" {
		t.Fatalf("preparing status = %q", recordedRequests[4].fields["text"])
	}
	if recordedRequests[6].fields["text"] != "Resposta <b>final</b>" {
		t.Fatalf("final text = %q", recordedRequests[6].fields["text"])
	}
	if recordedRequests[6].fields["parse_mode"] != "HTML" {
		t.Fatalf("final parse mode = %q", recordedRequests[6].fields["parse_mode"])
	}
}

func TestTelegramResponseProgressEditsFinalTableAsRichMessage(t *testing.T) {
	recordingHTTPClient := &recordingTelegramHTTPClient{}
	telegramBot, telegramBotError := bot.New(
		"test-token",
		bot.WithSkipGetMe(),
		bot.WithHTTPClient(time.Second, recordingHTTPClient),
	)
	if telegramBotError != nil {
		t.Fatalf("create Telegram bot: %v", telegramBotError)
	}
	progress := &telegramResponseProgress{
		telegramBot:     telegramBot,
		chatID:          123,
		statusMessageID: 77,
	}

	editError := progress.editFinalResponse(context.Background(), "| Lloc | Distància |\n|---|---:|\n| Sagrada Família | 50 m |")
	if editError != nil {
		t.Fatalf("edit rich response: %v", editError)
	}
	recordedRequests := recordingHTTPClient.recordedRequests()
	if len(recordedRequests) != 1 {
		t.Fatalf("recorded %d Telegram requests, want 1", len(recordedRequests))
	}
	var richMessage struct {
		HTML string `json:"html"`
	}
	if unmarshalError := json.Unmarshal([]byte(recordedRequests[0].fields["rich_message"]), &richMessage); unmarshalError != nil {
		t.Fatalf("decode rich_message: %v", unmarshalError)
	}
	if !strings.Contains(richMessage.HTML, "<table") {
		t.Fatalf("rich_message does not contain a table: %q", richMessage.HTML)
	}
}

// TestTelegramResponseProgressFinishesDraftAsFullPreview verifies a generated
// draft replaces the temporary status with a full separated preview and keeps
// the exact Mini App draft reference in its button.
func TestTelegramResponseProgressFinishesDraftAsFullPreview(t *testing.T) {
	recordingHTTPClient := &recordingTelegramHTTPClient{}
	telegramBot, telegramBotError := bot.New(
		"test-token",
		bot.WithSkipGetMe(),
		bot.WithHTTPClient(time.Second, recordingHTTPClient),
	)
	if telegramBotError != nil {
		t.Fatalf("create Telegram bot: %v", telegramBotError)
	}
	handler := &Handler{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		appBaseURL: "https://example.test",
	}
	progress := newTelegramResponseProgress(context.Background(), handler, telegramBot, 123, 0)
	contentJSON, contentJSONError := applicationModels.NewTiptapDocumentFromMarkdown("Bon dia,\n\n- **Et va bé** demà?\n- [Confirma-ho](https://example.com)")
	if contentJSONError != nil {
		t.Fatalf("create draft content: %v", contentJSONError)
	}
	draft := &applicationModels.Draft{ID: "2ee30369-f4ae-4741-8cfc-e58f2eb9b5f1", ContentJSON: contentJSON, BodyText: "Bon dia,\n\n- Et va bé demà?\n- Confirma-ho"}

	telegramMessageID := progress.finishDraftPreview(context.Background(), "private", draft, "T’he preparat aquesta proposta.")
	if telegramMessageID == nil || *telegramMessageID != 77 {
		t.Fatalf("finishDraftPreview message ID = %v, want 77", telegramMessageID)
	}
	recordedRequests := recordingHTTPClient.recordedRequests()
	if len(recordedRequests) != 3 {
		t.Fatalf("recorded %d Telegram requests, want 3: %#v", len(recordedRequests), recordedRequests)
	}
	if !strings.Contains(recordedRequests[2].fields["text"], "────────") || !strings.Contains(recordedRequests[2].fields["text"], "<b>Et va bé</b>") || !strings.Contains(recordedRequests[2].fields["text"], "<a href=\"https://example.com\">Confirma-ho</a>") {
		t.Errorf("draft preview = %q, want divider and formatted content", recordedRequests[2].fields["text"])
	}
	if !strings.Contains(recordedRequests[2].fields["reply_markup"], "https://example.test/miniapp?draft=2ee30369-f4ae-4741-8cfc-e58f2eb9b5f1") {
		t.Errorf("draft reply markup = %q, want exact draft URL", recordedRequests[2].fields["reply_markup"])
	}
}
