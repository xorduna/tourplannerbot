package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"tourplannerbot/internal/database"
	"tourplannerbot/internal/models"
	"tourplannerbot/internal/tools/bigin"
	"tourplannerbot/internal/tools/gmail"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

// GmailDraftService supplies the Mini App's explicit "move to Gmail" action.
// Its dependencies become ready after the process starts, just like the other
// Mini App stores, so all access is guarded by a mutex.
type GmailDraftService struct {
	mutex                 sync.RWMutex
	databaseConnection    *gorm.DB
	gmailClient           *gmail.Client
	biginClient           *bigin.Client
	trustedTelegramChatID int64
}

// NewGmailDraftService creates an initially unavailable Gmail draft service.
func NewGmailDraftService(trustedTelegramChatID int64) *GmailDraftService {
	return &GmailDraftService{trustedTelegramChatID: trustedTelegramChatID}
}

// SetDatabaseConnection enables deal-topic lookups after PostgreSQL starts.
func (service *GmailDraftService) SetDatabaseConnection(databaseConnection *gorm.DB) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.databaseConnection = databaseConnection
}

// SetGmailClient enables Gmail draft creation after OAuth configuration loads.
func (service *GmailDraftService) SetGmailClient(gmailClient *gmail.Client) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.gmailClient = gmailClient
}

// SetBiginClient enables deal email choices when Bigin is configured.
func (service *GmailDraftService) SetBiginClient(biginClient *bigin.Client) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.biginClient = biginClient
}

func (service *GmailDraftService) dependencies() (*gorm.DB, *gmail.Client, *bigin.Client) {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	return service.databaseConnection, service.gmailClient, service.biginClient
}

type gmailReplyCandidate struct {
	MessageID string `json:"message_id"`
	Subject   string `json:"subject,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Date      string `json:"date,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
}

type gmailOptionsResponse struct {
	ReplyAvailable bool                  `json:"reply_available"`
	Recipient      string                `json:"recipient,omitempty"`
	Candidates     []gmailReplyCandidate `json:"candidates"`
}

type createGmailDraftRequest struct {
	Mode      string `json:"mode"`
	To        string `json:"to"`
	MessageID string `json:"message_id"`
}

type createGmailDraftResponse struct {
	DraftID  string `json:"draft_id"`
	ThreadID string `json:"thread_id,omitempty"`
}

// RegisterGmailDraftRoutes registers routes even before integrations are
// ready. They return a clear temporary-unavailable response until then.
func RegisterGmailDraftRoutes(echoServer *echo.Echo, sessionAuthenticator *SessionAuthenticator, draftStore DraftStore, gmailDraftService *GmailDraftService) {
	if echoServer == nil || sessionAuthenticator == nil || draftStore == nil || gmailDraftService == nil {
		return
	}
	echoServer.GET("/api/drafts/:id/gmail-options", func(echoContext *echo.Context) error {
		return handleGmailOptions(sessionAuthenticator, draftStore, gmailDraftService, echoContext)
	})
	echoServer.POST("/api/drafts/:id/gmail-draft", func(echoContext *echo.Context) error {
		return handleCreateGmailDraft(sessionAuthenticator, draftStore, gmailDraftService, echoContext)
	})
}

func handleGmailOptions(sessionAuthenticator *SessionAuthenticator, draftStore DraftStore, service *GmailDraftService, echoContext *echo.Context) error {
	draft, _, response := authenticatedEmailDraft(sessionAuthenticator, draftStore, echoContext)
	if response != nil {
		return response
	}
	dealID, recipient, candidates, err := service.dealEmailOptions(echoContext.Request().Context(), draft)
	if err != nil {
		return fmt.Errorf("load Gmail options: %w", err)
	}
	return echoContext.JSON(http.StatusOK, gmailOptionsResponse{
		ReplyAvailable: dealID != "" && recipient != "",
		Recipient:      recipient,
		Candidates:     candidates,
	})
}

func handleCreateGmailDraft(sessionAuthenticator *SessionAuthenticator, draftStore DraftStore, service *GmailDraftService, echoContext *echo.Context) error {
	draft, _, response := authenticatedEmailDraft(sessionAuthenticator, draftStore, echoContext)
	if response != nil {
		return response
	}
	request := echoContext.Request()
	request.Body = http.MaxBytesReader(echoContext.Response(), request.Body, 4096)
	var createRequest createGmailDraftRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&createRequest); err != nil {
		return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "invalid Gmail draft request"})
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "invalid Gmail draft request"})
	}

	_, gmailClient, _ := service.dependencies()
	if gmailClient == nil {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Gmail is not configured"})
	}
	var createdDraft gmail.DraftResult
	var err error
	switch strings.TrimSpace(createRequest.Mode) {
	case "new":
		if strings.TrimSpace(createRequest.To) == "" {
			return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "a recipient is required"})
		}
		createdDraft, err = gmailClient.CreateDraft(request.Context(), gmail.DraftInput{
			To:      []string{strings.TrimSpace(createRequest.To)},
			Subject: draftSubject(draft),
			Body:    draft.BodyText,
		})
	case "reply":
		dealID, _, candidates, optionsError := service.dealEmailOptions(request.Context(), draft)
		if optionsError != nil {
			return fmt.Errorf("load deal reply choices: %w", optionsError)
		}
		if dealID == "" {
			return echoContext.JSON(http.StatusForbidden, map[string]string{"error": "replies are available only inside a deal topic"})
		}
		if !containsReplyCandidate(candidates, createRequest.MessageID) {
			return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "the selected email is not part of this contact's Gmail conversations"})
		}
		createdDraft, err = gmailClient.CreateReplyDraft(request.Context(), createRequest.MessageID, draft.BodyText)
	default:
		return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "invalid Gmail draft mode"})
	}
	if err != nil {
		echoContext.Logger().Warn("failed to create Gmail draft", "draft_id", draft.ID, "mode", createRequest.Mode, "error", err)
		return echoContext.JSON(http.StatusBadGateway, map[string]string{"error": "Gmail could not create the draft"})
	}
	return echoContext.JSON(http.StatusCreated, createGmailDraftResponse{DraftID: createdDraft.DraftID, ThreadID: createdDraft.ThreadID})
}

// authenticatedEmailDraft performs the shared authorization and makes the
// endpoint invisible for non-email drafts, just as it is for another user's draft.
func authenticatedEmailDraft(sessionAuthenticator *SessionAuthenticator, draftStore DraftStore, echoContext *echo.Context) (*models.Draft, int64, error) {
	draftID := echoContext.Param("id")
	if !draftUUIDPattern.MatchString(draftID) {
		return nil, 0, echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	telegramUserID, authenticationError := sessionAuthenticator.AuthenticateSessionRequest(echoContext.Request())
	if authenticationResponse := sessionAuthenticationErrorResponse(authenticationError, echoContext); authenticationResponse != nil {
		return nil, 0, authenticationResponse
	}
	draft, err := draftStore.FindAuthorizedDraft(echoContext.Request().Context(), draftID, telegramUserID)
	if errors.Is(err, database.ErrDraftNotFound) {
		return nil, 0, echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	if errors.Is(err, ErrDraftReaderUnavailable) {
		return nil, 0, echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "draft storage temporarily unavailable"})
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read authorized draft: %w", err)
	}
	if draft.Kind != models.DraftKindEmail {
		return nil, 0, echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	return draft, telegramUserID, nil
}

func (service *GmailDraftService) dealEmailOptions(ctx context.Context, draft *models.Draft) (string, string, []gmailReplyCandidate, error) {
	databaseConnection, gmailClient, biginClient := service.dependencies()
	if draft.ChatID != service.trustedTelegramChatID || draft.MessageThreadID <= 0 || databaseConnection == nil || biginClient == nil {
		return "", "", []gmailReplyCandidate{}, nil
	}
	topic, err := database.FindTelegramDealTopicByMessageThreadID(ctx, databaseConnection, int64(draft.MessageThreadID))
	if errors.Is(err, database.ErrTelegramDealTopicNotFound) {
		return "", "", []gmailReplyCandidate{}, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	deal, err := biginClient.GetDeal(ctx, topic.DealID)
	if err != nil {
		return "", "", nil, err
	}
	recipient := ""
	if contactID := dealContactID(deal); contactID != "" {
		contact, contactError := biginClient.GetContact(ctx, contactID)
		if contactError != nil {
			return "", "", nil, contactError
		}
		recipient = firstEmailAddress(contact)
	}
	if gmailClient == nil || recipient == "" {
		return topic.DealID, recipient, []gmailReplyCandidate{}, nil
	}
	messages, err := gmailClient.SearchContactMessages(ctx, recipient)
	if err != nil {
		return "", "", nil, err
	}
	candidates := make([]gmailReplyCandidate, 0, len(messages))
	for _, message := range messages {
		candidates = append(candidates, gmailReplyCandidate{
			MessageID: message.MessageID,
			Subject:   message.Subject,
			From:      message.From,
			To:        message.To,
			Date:      message.Date,
			Snippet:   message.Snippet,
		})
	}
	return topic.DealID, recipient, candidates, nil
}

func draftSubject(draft *models.Draft) string {
	if draft.Subject == nil {
		return ""
	}
	return strings.TrimSpace(*draft.Subject)
}

func containsReplyCandidate(candidates []gmailReplyCandidate, messageID string) bool {
	for _, candidate := range candidates {
		if candidate.MessageID == strings.TrimSpace(messageID) {
			return true
		}
	}
	return false
}

func firstEmailAddress(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return findEmailAddress(value)
}

// dealContactID finds Bigin's standard Contact_Name lookup without assuming
// any ordering or tenant-specific fields in the full deal payload.
func dealContactID(raw json.RawMessage) string {
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data) == 0 {
		return ""
	}
	for fieldName, fieldValue := range envelope.Data[0] {
		if !strings.EqualFold(fieldName, "Contact_Name") && !strings.EqualFold(fieldName, "Contact") {
			continue
		}
		lookup, ok := fieldValue.(map[string]any)
		if !ok {
			continue
		}
		if identifier, ok := lookup["id"].(string); ok && allDigits(identifier) {
			return identifier
		}
	}
	return ""
}

func allDigits(value string) bool {
	return value != "" && strings.IndexFunc(value, func(character rune) bool { return character < '0' || character > '9' }) < 0
}

func findEmailAddress(value any) string {
	switch typedValue := value.(type) {
	case map[string]any:
		for key, child := range typedValue {
			if strings.EqualFold(key, "email") {
				if email, ok := child.(string); ok && strings.Contains(email, "@") {
					return strings.TrimSpace(email)
				}
			}
		}
		for _, child := range typedValue {
			if email := findEmailAddress(child); email != "" {
				return email
			}
		}
	case []any:
		for _, child := range typedValue {
			if email := findEmailAddress(child); email != "" {
				return email
			}
		}
	}
	return ""
}
