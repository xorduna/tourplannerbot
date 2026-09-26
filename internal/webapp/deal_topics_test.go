package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"tourplannerbot/internal/database"
	databaseModels "tourplannerbot/internal/models"

	telegramBot "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"
	"github.com/labstack/echo/v5"
)

type dealTopicStoreStub struct {
	mutex           sync.Mutex
	topics          map[string]*databaseModels.TelegramDealTopic
	findCallCount   int
	createCallCount int
	deleteCallCount int
	createdDealID   string
	createdThreadID int64
	deletedDealID   string
}

type biginDealReaderStub struct {
	name      string
	callCount int
	dealID    string
}

type dealTopicIntroductionGeneratorStub struct {
	introduction string
	err          error
	callCount    int
	dealID       string
}

type telegramForumTopicCreatorStub struct {
	messageThreadID       int
	callCount             int
	chatID                any
	name                  string
	topicExists           bool
	verificationError     error
	verificationCallCount int
	introductionCallCount int
	introductionThreadID  int
	introductionText      string
	introductionPreview   *telegramModels.LinkPreviewOptions
	introductionError     error
}

// FindTelegramDealTopic returns the configured association or the repository's
// not-found sentinel.
func (dealTopicStore *dealTopicStoreStub) FindTelegramDealTopic(_ context.Context, dealID string) (*databaseModels.TelegramDealTopic, error) {
	dealTopicStore.mutex.Lock()
	defer dealTopicStore.mutex.Unlock()
	dealTopicStore.findCallCount++
	telegramDealTopic, exists := dealTopicStore.topics[dealID]
	if !exists {
		return nil, database.ErrTelegramDealTopicNotFound
	}
	return telegramDealTopic, nil
}

// CreateTelegramDealTopic records and returns a new association.
func (dealTopicStore *dealTopicStoreStub) CreateTelegramDealTopic(_ context.Context, dealID string, messageThreadID int64) (*databaseModels.TelegramDealTopic, error) {
	dealTopicStore.mutex.Lock()
	defer dealTopicStore.mutex.Unlock()
	dealTopicStore.createCallCount++
	dealTopicStore.createdDealID = dealID
	dealTopicStore.createdThreadID = messageThreadID
	telegramDealTopic := &databaseModels.TelegramDealTopic{DealID: dealID, MessageThreadID: messageThreadID}
	dealTopicStore.topics[dealID] = telegramDealTopic
	return telegramDealTopic, nil
}

// DeleteTelegramDealTopic removes the configured stale association.
func (dealTopicStore *dealTopicStoreStub) DeleteTelegramDealTopic(_ context.Context, dealID string) error {
	dealTopicStore.mutex.Lock()
	defer dealTopicStore.mutex.Unlock()
	dealTopicStore.deleteCallCount++
	dealTopicStore.deletedDealID = dealID
	delete(dealTopicStore.topics, dealID)
	return nil
}

// GetDeal returns a complete current Bigin response with the configured name.
func (dealReader *biginDealReaderStub) GetDeal(_ context.Context, dealID string) (json.RawMessage, error) {
	dealReader.callCount++
	dealReader.dealID = dealID
	encodedResponse, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": dealID, "Deal_Name": dealReader.name, "Stage": "Qualification"}}})
	return json.RawMessage(encodedResponse), err
}

// GenerateIntroduction returns the configured first topic message.
func (introductionGenerator *dealTopicIntroductionGeneratorStub) GenerateIntroduction(_ context.Context, dealID string, _ json.RawMessage) (string, error) {
	introductionGenerator.callCount++
	introductionGenerator.dealID = dealID
	return introductionGenerator.introduction, introductionGenerator.err
}

// CreateForumTopic records the Telegram request and returns a topic.
func (topicCreator *telegramForumTopicCreatorStub) CreateForumTopic(_ context.Context, parameters *telegramBot.CreateForumTopicParams) (*telegramModels.ForumTopic, error) {
	topicCreator.callCount++
	topicCreator.chatID = parameters.ChatID
	topicCreator.name = parameters.Name
	return &telegramModels.ForumTopic{MessageThreadID: topicCreator.messageThreadID, Name: parameters.Name}, nil
}

// SendChatAction reports whether the persisted Telegram topic still exists.
func (topicCreator *telegramForumTopicCreatorStub) SendChatAction(_ context.Context, _ *telegramBot.SendChatActionParams) (bool, error) {
	topicCreator.verificationCallCount++
	if topicCreator.verificationError != nil {
		return false, topicCreator.verificationError
	}
	return topicCreator.topicExists, nil
}

// SendMessage records the introductory message sent to a new topic.
func (topicCreator *telegramForumTopicCreatorStub) SendMessage(_ context.Context, parameters *telegramBot.SendMessageParams) (*telegramModels.Message, error) {
	topicCreator.introductionCallCount++
	topicCreator.introductionThreadID = parameters.MessageThreadID
	topicCreator.introductionText = parameters.Text
	topicCreator.introductionPreview = parameters.LinkPreviewOptions
	if topicCreator.introductionError != nil {
		return nil, topicCreator.introductionError
	}
	return &telegramModels.Message{ID: 1, MessageThreadID: parameters.MessageThreadID}, nil
}

func TestDealTopicEndpointVerifiesAndRedirectsExistingAssociation(t *testing.T) {
	dealTopicStore := &dealTopicStoreStub{topics: map[string]*databaseModels.TelegramDealTopic{
		"2034020000000489080": {DealID: "2034020000000489080", MessageThreadID: 456},
	}}
	biginDealReader := &biginDealReaderStub{name: "Must not be used"}
	telegramTopicCreator := &telegramForumTopicCreatorStub{messageThreadID: 999, topicExists: true}
	dealTopicService := newTestDealTopicService(t, dealTopicStore, biginDealReader, nil, telegramTopicCreator)
	echoServer := echo.New()
	RegisterDealTopicRoutes(echoServer, dealTopicService)

	request := httptest.NewRequest(http.MethodGet, "/deals/2034020000000489080/topic", nil)
	responseRecorder := httptest.NewRecorder()
	echoServer.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", responseRecorder.Code, http.StatusFound)
	}
	if location := responseRecorder.Header().Get("Location"); location != "https://t.me/c/1234567890/456" {
		t.Errorf("Location = %q, want private topic URL", location)
	}
	if biginDealReader.callCount != 0 || telegramTopicCreator.callCount != 0 || dealTopicStore.createCallCount != 0 || telegramTopicCreator.introductionCallCount != 0 {
		t.Errorf("creation calls = Bigin %d, Telegram %d, store creates %d, introductions %d; want all zero", biginDealReader.callCount, telegramTopicCreator.callCount, dealTopicStore.createCallCount, telegramTopicCreator.introductionCallCount)
	}
	if telegramTopicCreator.verificationCallCount != 1 {
		t.Errorf("Telegram verification calls = %d, want 1", telegramTopicCreator.verificationCallCount)
	}
}

func TestDealTopicEndpointCreatesAndPersistsMissingAssociation(t *testing.T) {
	dealTopicStore := &dealTopicStoreStub{topics: make(map[string]*databaseModels.TelegramDealTopic)}
	biginDealReader := &biginDealReaderStub{name: "Barcelona visit"}
	introductionGenerator := &dealTopicIntroductionGeneratorStub{introduction: "👋 Resum inicial del tour"}
	telegramTopicCreator := &telegramForumTopicCreatorStub{messageThreadID: 789, introductionError: errors.New("Telegram introduction delivery failed")}
	dealTopicService := newTestDealTopicService(t, dealTopicStore, biginDealReader, introductionGenerator, telegramTopicCreator)
	echoServer := echo.New()
	RegisterDealTopicRoutes(echoServer, dealTopicService)

	request := httptest.NewRequest(http.MethodGet, "/deals/2034020000000489080/topic", nil)
	responseRecorder := httptest.NewRecorder()
	echoServer.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body = %s", responseRecorder.Code, http.StatusFound, responseRecorder.Body.String())
	}
	if location := responseRecorder.Header().Get("Location"); location != "https://t.me/c/1234567890/789" {
		t.Errorf("Location = %q, want newly created private topic URL", location)
	}
	if biginDealReader.callCount != 1 || biginDealReader.dealID != "2034020000000489080" {
		t.Errorf("Bigin lookup = (%d, %q), want one lookup for requested deal", biginDealReader.callCount, biginDealReader.dealID)
	}
	if telegramTopicCreator.callCount != 1 || telegramTopicCreator.chatID != int64(-1001234567890) || telegramTopicCreator.name != "Barcelona visit" {
		t.Errorf("Telegram creation = count %d, chat %#v, name %q", telegramTopicCreator.callCount, telegramTopicCreator.chatID, telegramTopicCreator.name)
	}
	if dealTopicStore.createCallCount != 1 || dealTopicStore.createdDealID != "2034020000000489080" || dealTopicStore.createdThreadID != 789 {
		t.Errorf("stored association = count %d, deal %q, thread %d", dealTopicStore.createCallCount, dealTopicStore.createdDealID, dealTopicStore.createdThreadID)
	}
	if telegramTopicCreator.introductionPreview == nil || telegramTopicCreator.introductionPreview.IsDisabled == nil || !*telegramTopicCreator.introductionPreview.IsDisabled {
		t.Errorf("topic introduction must disable the Telegram link preview, got %#v", telegramTopicCreator.introductionPreview)
	}
	if introductionGenerator.callCount != 1 || telegramTopicCreator.introductionCallCount != 1 || telegramTopicCreator.introductionThreadID != 789 || telegramTopicCreator.introductionText != "👋 Resum inicial del tour" {
		t.Errorf("introduction = generator calls %d, sends %d, thread %d, text %q", introductionGenerator.callCount, telegramTopicCreator.introductionCallCount, telegramTopicCreator.introductionThreadID, telegramTopicCreator.introductionText)
	}
}

func TestDealTopicEndpointUsesFallbackWhenIntroductionGenerationFails(t *testing.T) {
	dealTopicStore := &dealTopicStoreStub{topics: make(map[string]*databaseModels.TelegramDealTopic)}
	biginDealReader := &biginDealReaderStub{name: "Barcelona visit"}
	introductionGenerator := &dealTopicIntroductionGeneratorStub{err: errors.New("LLM unavailable")}
	telegramTopicCreator := &telegramForumTopicCreatorStub{messageThreadID: 789}
	dealTopicService := newTestDealTopicService(t, dealTopicStore, biginDealReader, introductionGenerator, telegramTopicCreator)

	topicURL, err := dealTopicService.ResolveTopicURL(context.Background(), "2034020000000489080")
	if err != nil {
		t.Fatalf("ResolveTopicURL returned error: %v", err)
	}
	if topicURL != "https://t.me/c/1234567890/789" {
		t.Errorf("topic URL = %q", topicURL)
	}
	if telegramTopicCreator.introductionCallCount != 1 || !strings.Contains(telegramTopicCreator.introductionText, "Barcelona visit") || !strings.Contains(telegramTopicCreator.introductionText, "2034020000000489080") {
		t.Errorf("fallback introduction = calls %d, text %q", telegramTopicCreator.introductionCallCount, telegramTopicCreator.introductionText)
	}
}

func TestDealTopicEndpointRecreatesMappingWhenTelegramTopicWasDeleted(t *testing.T) {
	dealTopicStore := &dealTopicStoreStub{topics: map[string]*databaseModels.TelegramDealTopic{
		"2034020000000489080": {DealID: "2034020000000489080", MessageThreadID: 456},
	}}
	biginDealReader := &biginDealReaderStub{name: "Barcelona visit"}
	telegramTopicCreator := &telegramForumTopicCreatorStub{
		messageThreadID:   789,
		verificationError: fmt.Errorf("%w, message thread not found", telegramBot.ErrorBadRequest),
	}
	dealTopicService := newTestDealTopicService(t, dealTopicStore, biginDealReader, nil, telegramTopicCreator)
	echoServer := echo.New()
	RegisterDealTopicRoutes(echoServer, dealTopicService)

	request := httptest.NewRequest(http.MethodGet, "/deals/2034020000000489080/topic", nil)
	responseRecorder := httptest.NewRecorder()
	echoServer.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body = %s", responseRecorder.Code, http.StatusFound, responseRecorder.Body.String())
	}
	if location := responseRecorder.Header().Get("Location"); location != "https://t.me/c/1234567890/789" {
		t.Errorf("Location = %q, want recreated private topic URL", location)
	}
	if dealTopicStore.deleteCallCount != 1 || dealTopicStore.deletedDealID != "2034020000000489080" {
		t.Errorf("deleted mapping = count %d, deal %q", dealTopicStore.deleteCallCount, dealTopicStore.deletedDealID)
	}
	if dealTopicStore.createCallCount != 1 || telegramTopicCreator.callCount != 1 || biginDealReader.callCount != 1 || telegramTopicCreator.introductionCallCount != 1 {
		t.Errorf("recreation calls = store %d, Telegram %d, Bigin %d, introductions %d; want one each", dealTopicStore.createCallCount, telegramTopicCreator.callCount, biginDealReader.callCount, telegramTopicCreator.introductionCallCount)
	}
}

func TestDealTopicResolutionPreservesMappingOnTransientTelegramError(t *testing.T) {
	dealTopicStore := &dealTopicStoreStub{topics: map[string]*databaseModels.TelegramDealTopic{
		"2034020000000489080": {DealID: "2034020000000489080", MessageThreadID: 456},
	}}
	telegramTopicCreator := &telegramForumTopicCreatorStub{verificationError: errors.New("temporary Telegram outage")}
	dealTopicService := newTestDealTopicService(t, dealTopicStore, &biginDealReaderStub{name: "Unused"}, nil, telegramTopicCreator)

	if _, err := dealTopicService.ResolveTopicURL(context.Background(), "2034020000000489080"); err == nil {
		t.Fatal("ResolveTopicURL returned nil error for transient Telegram failure")
	}
	if dealTopicStore.deleteCallCount != 0 || dealTopicStore.createCallCount != 0 {
		t.Errorf("mapping changed after transient error: deletes %d, creates %d", dealTopicStore.deleteCallCount, dealTopicStore.createCallCount)
	}
}

func TestDealTopicEndpointRejectsInvalidDealIDAndReportsUnavailableDependencies(t *testing.T) {
	dealTopicStore := &dealTopicStoreStub{topics: make(map[string]*databaseModels.TelegramDealTopic)}
	dealTopicService := newTestDealTopicService(t, dealTopicStore, nil, nil, nil)
	echoServer := echo.New()
	RegisterDealTopicRoutes(echoServer, dealTopicService)

	testCases := []struct {
		path       string
		wantStatus int
	}{
		{path: "/deals/not-a-number/topic", wantStatus: http.StatusBadRequest},
		{path: "/deals/2034020000000489080/topic", wantStatus: http.StatusServiceUnavailable},
	}
	for _, testCase := range testCases {
		request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
		responseRecorder := httptest.NewRecorder()
		echoServer.ServeHTTP(responseRecorder, request)
		if responseRecorder.Code != testCase.wantStatus {
			t.Errorf("GET %s status = %d, want %d", testCase.path, responseRecorder.Code, testCase.wantStatus)
		}
	}
}

// newTestDealTopicService builds a configured topic service for HTTP tests.
func newTestDealTopicService(testingHandle *testing.T, dealTopicStore DealTopicStore, biginDealReader BiginDealReader, introductionGenerator DealTopicIntroductionGenerator, telegramTopicCreator TelegramForumTopicCreator) *DealTopicService {
	testingHandle.Helper()
	dealTopicService, err := NewDealTopicService(-1001234567890, dealTopicStore)
	if err != nil {
		testingHandle.Fatalf("NewDealTopicService returned error: %v", err)
	}
	dealTopicService.SetBiginDealReader(biginDealReader)
	dealTopicService.SetIntroductionGenerator(introductionGenerator)
	dealTopicService.SetTelegramForumTopicCreator(telegramTopicCreator)
	dealTopicService.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return dealTopicService
}
