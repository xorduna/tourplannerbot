package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"tourplannerbot/internal/database"
	"tourplannerbot/internal/models"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

var (
	// ErrDraftReaderUnavailable indicates that PostgreSQL is not connected yet.
	ErrDraftReaderUnavailable = errors.New("draft reader is unavailable")
	draftUUIDPattern          = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

const maximumDraftSubjectRunes = 200

// DraftStore retrieves and updates drafts only when they belong to the
// authenticated Telegram user. It deliberately does not reveal another user's draft.
type DraftStore interface {
	FindAuthorizedDraft(applicationContext context.Context, draftID string, telegramUserID int64) (*models.Draft, error)
	UpdateAuthorizedDraft(applicationContext context.Context, updateDraftInput database.UpdateDraftInput) (*models.Draft, error)
}

// GORMDraftReader provides the Mini App with a database-backed draft reader.
// Its connection is set after the startup retry loop succeeds.
type GORMDraftReader struct {
	databaseConnectionMutex sync.RWMutex
	databaseConnection      *gorm.DB
}

// NewGORMDraftReader creates a draft reader without a database connection.
func NewGORMDraftReader() *GORMDraftReader {
	return &GORMDraftReader{}
}

// SetDatabaseConnection makes the supplied GORM connection available for draft reads.
func (draftReader *GORMDraftReader) SetDatabaseConnection(databaseConnection *gorm.DB) {
	draftReader.databaseConnectionMutex.Lock()
	defer draftReader.databaseConnectionMutex.Unlock()
	draftReader.databaseConnection = databaseConnection
}

// FindAuthorizedDraft returns a draft only when it is owned by telegramUserID.
// An existing draft owned by another user is intentionally indistinguishable
// from a missing draft.
func (draftReader *GORMDraftReader) FindAuthorizedDraft(applicationContext context.Context, draftID string, telegramUserID int64) (*models.Draft, error) {
	draftReader.databaseConnectionMutex.RLock()
	databaseConnection := draftReader.databaseConnection
	draftReader.databaseConnectionMutex.RUnlock()
	if databaseConnection == nil {
		return nil, ErrDraftReaderUnavailable
	}
	draft, err := database.FindDraftByID(applicationContext, databaseConnection, draftID)
	if err != nil {
		return nil, err
	}
	if draft.OwnerTelegramID != telegramUserID {
		return nil, database.ErrDraftNotFound
	}
	return draft, nil
}

// UpdateAuthorizedDraft performs an owner-scoped optimistic update.
func (draftReader *GORMDraftReader) UpdateAuthorizedDraft(applicationContext context.Context, updateDraftInput database.UpdateDraftInput) (*models.Draft, error) {
	draftReader.databaseConnectionMutex.RLock()
	databaseConnection := draftReader.databaseConnection
	draftReader.databaseConnectionMutex.RUnlock()
	if databaseConnection == nil {
		return nil, ErrDraftReaderUnavailable
	}
	return database.UpdateDraft(applicationContext, databaseConnection, updateDraftInput)
}

type draftResponse struct {
	ID        string           `json:"id"`
	Kind      models.DraftKind `json:"kind"`
	Subject   *string          `json:"subject"`
	Content   json.RawMessage  `json:"content"`
	BodyText  string           `json:"body_text"`
	Revision  int              `json:"revision"`
	UpdatedAt string           `json:"updated_at"`
}

type updateDraftRequest struct {
	ExpectedRevision int             `json:"expected_revision"`
	Subject          *string         `json:"subject"`
	Content          json.RawMessage `json:"content"`
}

type draftConflictResponse struct {
	Error   string        `json:"error"`
	Current draftResponse `json:"current"`
}

// registerDraftRoutes registers the read-only Mini App draft API for slice 5.
func registerDraftRoutes(echoServer *echo.Echo, sessionAuthenticator *SessionAuthenticator, draftStore DraftStore) {
	echoServer.GET("/api/drafts/:id", func(echoContext *echo.Context) error {
		return handleGetDraft(sessionAuthenticator, draftStore, echoContext)
	})
	echoServer.PATCH("/api/drafts/:id", func(echoContext *echo.Context) error {
		return handlePatchDraft(sessionAuthenticator, draftStore, echoContext)
	})
}

// handleGetDraft authenticates the API session before returning one authorized draft.
func handleGetDraft(sessionAuthenticator *SessionAuthenticator, draftStore DraftStore, echoContext *echo.Context) error {
	draftID := echoContext.Param("id")
	if !draftUUIDPattern.MatchString(draftID) {
		return echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	telegramUserID, err := sessionAuthenticator.AuthenticateSessionRequest(echoContext.Request())
	if errors.Is(err, ErrInvalidTelegramInitData) {
		return echoContext.JSON(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if errors.Is(err, ErrTelegramUserNotAllowed) {
		return echoContext.JSON(http.StatusForbidden, map[string]string{"error": "Telegram user is not allowed"})
	}
	if err != nil {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "authorization temporarily unavailable"})
	}

	draft, err := draftStore.FindAuthorizedDraft(echoContext.Request().Context(), draftID, telegramUserID)
	if errors.Is(err, database.ErrDraftNotFound) {
		return echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	if errors.Is(err, ErrDraftReaderUnavailable) {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "draft storage temporarily unavailable"})
	}
	if err != nil {
		return fmt.Errorf("read authorized draft: %w", err)
	}

	return echoContext.JSON(http.StatusOK, newDraftResponse(draft))
}

// handlePatchDraft validates editor content and atomically saves it against the
// expected revision, returning a conflict rather than overwriting newer work.
func handlePatchDraft(sessionAuthenticator *SessionAuthenticator, draftStore DraftStore, echoContext *echo.Context) error {
	draftID := echoContext.Param("id")
	if !draftUUIDPattern.MatchString(draftID) {
		return echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	telegramUserID, authenticationError := sessionAuthenticator.AuthenticateSessionRequest(echoContext.Request())
	if authenticationResponse := sessionAuthenticationErrorResponse(authenticationError, echoContext); authenticationResponse != nil {
		return authenticationResponse
	}

	request := echoContext.Request()
	request.Body = http.MaxBytesReader(echoContext.Response(), request.Body, models.MaximumDraftDocumentBytes+1024)
	var updateRequest updateDraftRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&updateRequest); err != nil {
		return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "invalid draft update"})
	}
	if updateRequest.ExpectedRevision < 1 || (updateRequest.Subject != nil && len([]rune(strings.TrimSpace(*updateRequest.Subject))) > maximumDraftSubjectRunes) {
		return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "invalid draft update"})
	}
	bodyText, err := models.ValidateAndProjectTiptapDocument(updateRequest.Content)
	if err != nil {
		return echoContext.JSON(http.StatusBadRequest, map[string]string{"error": "invalid draft content"})
	}
	updatedDraft, err := draftStore.UpdateAuthorizedDraft(echoContext.Request().Context(), database.UpdateDraftInput{
		ID:               draftID,
		OwnerTelegramID:  telegramUserID,
		ExpectedRevision: updateRequest.ExpectedRevision,
		Subject:          updateRequest.Subject,
		ContentJSON:      updateRequest.Content,
		BodyText:         bodyText,
	})
	var revisionConflict *database.DraftRevisionConflictError
	if errors.As(err, &revisionConflict) {
		return echoContext.JSON(http.StatusConflict, draftConflictResponse{Error: "draft revision conflict", Current: newDraftResponse(revisionConflict.CurrentDraft)})
	}
	if errors.Is(err, database.ErrDraftNotFound) {
		return echoContext.JSON(http.StatusNotFound, map[string]string{"error": "draft not found"})
	}
	if errors.Is(err, ErrDraftReaderUnavailable) {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "draft storage temporarily unavailable"})
	}
	if err != nil {
		return fmt.Errorf("update authorized draft: %w", err)
	}
	return echoContext.JSON(http.StatusOK, newDraftResponse(updatedDraft))
}

// sessionAuthenticationErrorResponse converts session validation errors to the
// consistent API responses shared by draft reads and writes.
func sessionAuthenticationErrorResponse(authenticationError error, echoContext *echo.Context) error {
	if errors.Is(authenticationError, ErrInvalidTelegramInitData) {
		return echoContext.JSON(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if errors.Is(authenticationError, ErrTelegramUserNotAllowed) {
		return echoContext.JSON(http.StatusForbidden, map[string]string{"error": "Telegram user is not allowed"})
	}
	if authenticationError != nil {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "authorization temporarily unavailable"})
	}
	return nil
}

// newDraftResponse maps a database draft to the API's public shape.
func newDraftResponse(draft *models.Draft) draftResponse {
	return draftResponse{
		ID:        draft.ID,
		Kind:      draft.Kind,
		Subject:   draft.Subject,
		Content:   draft.ContentJSON,
		BodyText:  draft.BodyText,
		Revision:  draft.Revision,
		UpdatedAt: draft.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}
