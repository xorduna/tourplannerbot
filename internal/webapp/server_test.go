package webapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"tourplannerbot/internal/buildinfo"
	"tourplannerbot/internal/database"
	"tourplannerbot/internal/models"
)

var testBuildInformation = buildinfo.Information{
	Version:   "main_abcdef0",
	BuildTime: "2026-09-20T10:30:00Z",
}

type readinessCheckerFunc func(applicationContext context.Context) error

type allowedUserAuthorizerFunc func(applicationContext context.Context, telegramUserID int64) (bool, error)

type draftReaderFunc func(applicationContext context.Context, draftID string, telegramUserID int64) (*models.Draft, error)

type draftStoreStub struct {
	findDraft   func(applicationContext context.Context, draftID string, telegramUserID int64) (*models.Draft, error)
	updateDraft func(applicationContext context.Context, updateDraftInput database.UpdateDraftInput) (*models.Draft, error)
}

// Check calls the function supplied by the test.
func (function readinessCheckerFunc) Check(applicationContext context.Context) error {
	return function(applicationContext)
}

// IsAllowed calls the function supplied by the test.
func (function allowedUserAuthorizerFunc) IsAllowed(applicationContext context.Context, telegramUserID int64) (bool, error) {
	return function(applicationContext, telegramUserID)
}

// FindAuthorizedDraft calls the function supplied by the test.
func (function draftReaderFunc) FindAuthorizedDraft(applicationContext context.Context, draftID string, telegramUserID int64) (*models.Draft, error) {
	return function(applicationContext, draftID, telegramUserID)
}

// UpdateAuthorizedDraft rejects writes in read-only draft-reader tests.
func (function draftReaderFunc) UpdateAuthorizedDraft(applicationContext context.Context, updateDraftInput database.UpdateDraftInput) (*models.Draft, error) {
	return nil, errors.New("draft updates are not configured for this test")
}

// FindAuthorizedDraft calls the draft-store function supplied by the test.
func (draftStore draftStoreStub) FindAuthorizedDraft(applicationContext context.Context, draftID string, telegramUserID int64) (*models.Draft, error) {
	return draftStore.findDraft(applicationContext, draftID, telegramUserID)
}

// UpdateAuthorizedDraft calls the draft-store function supplied by the test.
func (draftStore draftStoreStub) UpdateAuthorizedDraft(applicationContext context.Context, updateDraftInput database.UpdateDraftInput) (*models.Draft, error) {
	return draftStore.updateDraft(applicationContext, updateDraftInput)
}

// newTestServer creates an Echo server without writing request logs in test output.
func newTestServer(readinessChecker ReadinessChecker) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(logger, readinessChecker, testBuildInformation, nil, nil)
}

// newTestSessionAuthenticator creates an authenticator with stable time for HTTP tests.
func newTestSessionAuthenticator(testingHandle *testing.T, allowedUserAuthorizer AllowedUserAuthorizer) *SessionAuthenticator {
	testingHandle.Helper()
	sessionAuthenticator, err := NewSessionAuthenticator(SessionConfig{
		TelegramBotToken:     "test-telegram-token",
		AuthenticationMaxAge: 5 * time.Minute,
		Clock: func() time.Time {
			return time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
		},
	}, allowedUserAuthorizer)
	if err != nil {
		testingHandle.Fatalf("NewSessionAuthenticator returned error: %v", err)
	}
	return sessionAuthenticator
}

func TestHandlerReturnsLivenessWithoutCheckingDependencies(t *testing.T) {
	readinessCheckCalls := 0
	handler := newTestServer(readinessCheckerFunc(func(applicationContext context.Context) error {
		readinessCheckCalls++
		return errors.New("database unavailable")
	}))

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	responseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", responseRecorder.Code, http.StatusOK)
	}
	wantBody := "{\"status\":\"ok\",\"version\":\"main_abcdef0\",\"build_time\":\"2026-09-20T10:30:00Z\"}\n"
	if responseRecorder.Body.String() != wantBody {
		t.Errorf("GET /healthz body = %q, want %q", responseRecorder.Body.String(), wantBody)
	}
	if readinessCheckCalls != 0 {
		t.Errorf("GET /healthz readiness checks = %d, want 0", readinessCheckCalls)
	}
}

func TestHandlerReturnsReadinessStatus(t *testing.T) {
	testCases := []struct {
		name                string
		readinessCheckError error
		wantStatus          int
		wantBody            string
	}{
		{
			name:       "database available",
			wantStatus: http.StatusOK,
			wantBody:   "ok\n",
		},
		{
			name:                "database unavailable",
			readinessCheckError: errors.New("database unavailable"),
			wantStatus:          http.StatusServiceUnavailable,
			wantBody:            "database unavailable\n",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := newTestServer(readinessCheckerFunc(func(applicationContext context.Context) error {
				return testCase.readinessCheckError
			}))
			request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			responseRecorder := httptest.NewRecorder()

			handler.ServeHTTP(responseRecorder, request)

			if responseRecorder.Code != testCase.wantStatus {
				t.Errorf("GET /readyz status = %d, want %d", responseRecorder.Code, testCase.wantStatus)
			}
			if responseRecorder.Body.String() != testCase.wantBody {
				t.Errorf("GET /readyz body = %q, want %q", responseRecorder.Body.String(), testCase.wantBody)
			}
		})
	}
}

func TestServerLogsEachRequest(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	server := NewServer(logger, readinessCheckerFunc(func(applicationContext context.Context) error {
		return nil
	}), testBuildInformation, newTestSessionAuthenticator(t, allowedUserAuthorizerFunc(func(applicationContext context.Context, telegramUserID int64) (bool, error) {
		return true, nil
	})), nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	responseRecorder := httptest.NewRecorder()

	server.ServeHTTP(responseRecorder, request)

	if !strings.Contains(logOutput.String(), "msg=REQUEST") {
		t.Errorf("Echo request log = %q, want a REQUEST entry", logOutput.String())
	}
	if !strings.Contains(logOutput.String(), "status=200") {
		t.Errorf("Echo request log = %q, want status=200", logOutput.String())
	}
}

func TestMiniAppSessionHandshakeValidatesTelegramAndSetsSessionCookie(t *testing.T) {
	sessionAuthenticator := newTestSessionAuthenticator(t, allowedUserAuthorizerFunc(func(applicationContext context.Context, telegramUserID int64) (bool, error) {
		return telegramUserID == 42, nil
	}))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(logger, readinessCheckerFunc(func(applicationContext context.Context) error { return nil }), testBuildInformation, sessionAuthenticator, nil)
	initData := signedTestInitData(t, TelegramUser{ID: 42, FirstName: "Diana", Username: "diana"}, time.Date(2026, time.September, 20, 11, 59, 0, 0, time.UTC))
	request := httptest.NewRequest(http.MethodPost, "/api/miniapp/session", strings.NewReader(`{"init_data":`+jsonQuote(initData)+`}`))
	request.Header.Set("Content-Type", "application/json")
	responseRecorder := httptest.NewRecorder()

	server.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("POST /api/miniapp/session status = %d, want %d; body = %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"first_name":"Diana"`) {
		t.Errorf("session response = %q, want authenticated identity", responseRecorder.Body.String())
	}
	cookieHeaders := responseRecorder.Result().Cookies()
	if len(cookieHeaders) != 1 {
		t.Fatalf("session cookie count = %d, want 1", len(cookieHeaders))
	}
	if cookieHeaders[0].Name != miniAppSessionCookieName || !cookieHeaders[0].HttpOnly || !cookieHeaders[0].Secure || cookieHeaders[0].Path != "/api" {
		t.Errorf("session cookie = %#v, want secure HttpOnly API cookie", cookieHeaders[0])
	}
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/future-protected-route", nil)
	authenticatedRequest.AddCookie(cookieHeaders[0])
	telegramUserID, err := sessionAuthenticator.AuthenticateSessionRequest(authenticatedRequest)
	if err != nil || telegramUserID != 42 {
		t.Errorf("AuthenticateSessionRequest = (%d, %v), want (42, nil)", telegramUserID, err)
	}
	tamperedRequest := httptest.NewRequest(http.MethodGet, "/api/future-protected-route", nil)
	tamperedCookie := *cookieHeaders[0]
	tamperedCookie.Value += "x"
	tamperedRequest.AddCookie(&tamperedCookie)
	if _, err := sessionAuthenticator.AuthenticateSessionRequest(tamperedRequest); !errors.Is(err, ErrInvalidTelegramInitData) {
		t.Errorf("AuthenticateSessionRequest tampered cookie error = %v, want ErrInvalidTelegramInitData", err)
	}
}

func TestMiniAppSessionHandshakeRejectsInvalidExpiredAndUnauthorizedRequests(t *testing.T) {
	currentTime := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name       string
		initData   string
		allowed    bool
		wantStatus int
	}{
		{name: "missing credentials", initData: "", allowed: true, wantStatus: http.StatusUnauthorized},
		{name: "tampered credentials", initData: signedTestInitData(t, TelegramUser{ID: 42, FirstName: "Diana"}, currentTime) + "x", allowed: true, wantStatus: http.StatusUnauthorized},
		{name: "expired credentials", initData: signedTestInitData(t, TelegramUser{ID: 42, FirstName: "Diana"}, currentTime.Add(-6*time.Minute)), allowed: true, wantStatus: http.StatusUnauthorized},
		{name: "unallowed user", initData: signedTestInitData(t, TelegramUser{ID: 42, FirstName: "Diana"}, currentTime), allowed: false, wantStatus: http.StatusForbidden},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sessionAuthenticator := newTestSessionAuthenticator(t, allowedUserAuthorizerFunc(func(applicationContext context.Context, telegramUserID int64) (bool, error) {
				return testCase.allowed, nil
			}))
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			server := NewServer(logger, readinessCheckerFunc(func(applicationContext context.Context) error { return nil }), testBuildInformation, sessionAuthenticator, nil)
			request := httptest.NewRequest(http.MethodPost, "/api/miniapp/session", strings.NewReader(`{"init_data":`+jsonQuote(testCase.initData)+`}`))
			request.Header.Set("Content-Type", "application/json")
			responseRecorder := httptest.NewRecorder()

			server.ServeHTTP(responseRecorder, request)

			if responseRecorder.Code != testCase.wantStatus {
				t.Errorf("POST /api/miniapp/session status = %d, want %d; body = %s", responseRecorder.Code, testCase.wantStatus, responseRecorder.Body.String())
			}
		})
	}
}

func TestDraftAPIRequiresSessionAndDoesNotLeakUnauthorizedDrafts(t *testing.T) {
	sessionAuthenticator := newTestSessionAuthenticator(t, allowedUserAuthorizerFunc(func(applicationContext context.Context, telegramUserID int64) (bool, error) {
		return telegramUserID == 42, nil
	}))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	draftID := "0193a67a-4ae4-4e2c-9e94-537889065d11"
	server := NewServer(logger, readinessCheckerFunc(func(applicationContext context.Context) error { return nil }), testBuildInformation, sessionAuthenticator, draftReaderFunc(func(applicationContext context.Context, requestedDraftID string, telegramUserID int64) (*models.Draft, error) {
		if requestedDraftID != draftID || telegramUserID != 42 {
			return nil, database.ErrDraftNotFound
		}
		return &models.Draft{
			ID:          draftID,
			Kind:        models.DraftKindWhatsApp,
			ContentJSON: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Private draft"}]}]}`),
			BodyText:    "Private draft",
			Revision:    1,
		}, nil
	}))

	unauthenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/drafts/"+draftID, nil)
	unauthenticatedResponseRecorder := httptest.NewRecorder()
	server.ServeHTTP(unauthenticatedResponseRecorder, unauthenticatedRequest)
	if unauthenticatedResponseRecorder.Code != http.StatusUnauthorized {
		t.Errorf("GET draft without session status = %d, want %d", unauthenticatedResponseRecorder.Code, http.StatusUnauthorized)
	}

	responseRecorder := httptest.NewRecorder()
	if err := sessionAuthenticator.setSessionCookie(responseRecorder, 42); err != nil {
		t.Fatalf("setSessionCookie returned error: %v", err)
	}
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/drafts/"+draftID, nil)
	authenticatedRequest.AddCookie(responseRecorder.Result().Cookies()[0])
	authenticatedResponseRecorder := httptest.NewRecorder()
	server.ServeHTTP(authenticatedResponseRecorder, authenticatedRequest)
	if authenticatedResponseRecorder.Code != http.StatusOK {
		t.Fatalf("GET authorized draft status = %d, want %d; body = %s", authenticatedResponseRecorder.Code, http.StatusOK, authenticatedResponseRecorder.Body.String())
	}
	if !strings.Contains(authenticatedResponseRecorder.Body.String(), `"text":"Private draft"`) {
		t.Errorf("GET authorized draft body = %q, want Tiptap document", authenticatedResponseRecorder.Body.String())
	}

	notFoundRequest := httptest.NewRequest(http.MethodGet, "/api/drafts/0193a67a-4ae4-4e2c-9e94-537889065d12", nil)
	notFoundRequest.AddCookie(responseRecorder.Result().Cookies()[0])
	notFoundResponseRecorder := httptest.NewRecorder()
	server.ServeHTTP(notFoundResponseRecorder, notFoundRequest)
	if notFoundResponseRecorder.Code != http.StatusNotFound {
		t.Errorf("GET unauthorized draft status = %d, want %d", notFoundResponseRecorder.Code, http.StatusNotFound)
	}
	if strings.Contains(notFoundResponseRecorder.Body.String(), "Private draft") {
		t.Errorf("GET unauthorized draft body leaked content: %q", notFoundResponseRecorder.Body.String())
	}
}

func TestDraftAPIUpdatesValidatedContentAndReturnsRevisionConflict(t *testing.T) {
	sessionAuthenticator := newTestSessionAuthenticator(t, allowedUserAuthorizerFunc(func(applicationContext context.Context, telegramUserID int64) (bool, error) {
		return telegramUserID == 42, nil
	}))
	draftID := "0193a67a-4ae4-4e2c-9e94-537889065d11"
	updatedInput := database.UpdateDraftInput{}
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), readinessCheckerFunc(func(applicationContext context.Context) error { return nil }), testBuildInformation, sessionAuthenticator, draftStoreStub{
		findDraft: func(applicationContext context.Context, requestedDraftID string, telegramUserID int64) (*models.Draft, error) {
			return nil, database.ErrDraftNotFound
		},
		updateDraft: func(applicationContext context.Context, updateDraftInput database.UpdateDraftInput) (*models.Draft, error) {
			updatedInput = updateDraftInput
			if updateDraftInput.ExpectedRevision == 1 {
				return &models.Draft{ID: draftID, Kind: models.DraftKindGeneric, ContentJSON: updateDraftInput.ContentJSON, BodyText: updateDraftInput.BodyText, Revision: 2}, nil
			}
			return nil, &database.DraftRevisionConflictError{CurrentDraft: &models.Draft{ID: draftID, Kind: models.DraftKindGeneric, ContentJSON: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Newer text"}]}]}`), BodyText: "Newer text", Revision: 3}}
		},
	})
	cookieRecorder := httptest.NewRecorder()
	if err := sessionAuthenticator.setSessionCookie(cookieRecorder, 42); err != nil {
		t.Fatalf("set session cookie: %v", err)
	}
	sessionCookie := cookieRecorder.Result().Cookies()[0]
	contentJSON := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Updated text","marks":[{"type":"bold"}]}]}]}`
	request := httptest.NewRequest(http.MethodPatch, "/api/drafts/"+draftID, strings.NewReader(`{"expected_revision":1,"subject":"A subject","content":`+contentJSON+`}`))
	request.AddCookie(sessionCookie)
	responseRecorder := httptest.NewRecorder()
	server.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH draft status = %d, want %d; body = %s", responseRecorder.Code, http.StatusOK, responseRecorder.Body.String())
	}
	if updatedInput.BodyText != "Updated text" || updatedInput.ExpectedRevision != 1 || updatedInput.OwnerTelegramID != 42 {
		t.Errorf("UpdateAuthorizedDraft input = %#v, want server-projected text and authenticated owner", updatedInput)
	}
	if !strings.Contains(responseRecorder.Body.String(), `"revision":2`) {
		t.Errorf("PATCH draft response = %q, want revision two", responseRecorder.Body.String())
	}

	conflictRequest := httptest.NewRequest(http.MethodPatch, "/api/drafts/"+draftID, strings.NewReader(`{"expected_revision":2,"content":`+contentJSON+`}`))
	conflictRequest.AddCookie(sessionCookie)
	conflictResponseRecorder := httptest.NewRecorder()
	server.ServeHTTP(conflictResponseRecorder, conflictRequest)
	if conflictResponseRecorder.Code != http.StatusConflict || !strings.Contains(conflictResponseRecorder.Body.String(), `"revision":3`) {
		t.Errorf("PATCH stale draft response = %d %q, want 409 with current revision", conflictResponseRecorder.Code, conflictResponseRecorder.Body.String())
	}

	invalidContentRequest := httptest.NewRequest(http.MethodPatch, "/api/drafts/"+draftID, strings.NewReader(`{"expected_revision":3,"content":{"type":"doc","content":[{"type":"heading"}]}}`))
	invalidContentRequest.AddCookie(sessionCookie)
	invalidContentResponseRecorder := httptest.NewRecorder()
	server.ServeHTTP(invalidContentResponseRecorder, invalidContentRequest)
	if invalidContentResponseRecorder.Code != http.StatusBadRequest {
		t.Errorf("PATCH invalid content status = %d, want %d", invalidContentResponseRecorder.Code, http.StatusBadRequest)
	}
}

// signedTestInitData creates launch data using Telegram's documented HMAC scheme.
func signedTestInitData(testingHandle *testing.T, telegramUser TelegramUser, authenticatedAt time.Time) string {
	testingHandle.Helper()
	userJSON, err := json.Marshal(telegramUser)
	if err != nil {
		testingHandle.Fatalf("marshal Telegram user: %v", err)
	}
	queryValues := url.Values{}
	queryValues.Set("auth_date", strconv.FormatInt(authenticatedAt.Unix(), 10))
	queryValues.Set("query_id", "test-query")
	queryValues.Set("user", string(userJSON))
	dataCheckValues := make([]string, 0, len(queryValues))
	for parameterName, parameterValues := range queryValues {
		dataCheckValues = append(dataCheckValues, parameterName+"="+parameterValues[0])
	}
	sort.Strings(dataCheckValues)
	secretKeyMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretKeyMAC.Write([]byte("test-telegram-token"))
	dataCheckMAC := hmac.New(sha256.New, secretKeyMAC.Sum(nil))
	_, _ = dataCheckMAC.Write([]byte(strings.Join(dataCheckValues, "\n")))
	queryValues.Set("hash", hex.EncodeToString(dataCheckMAC.Sum(nil)))
	return queryValues.Encode()
}

// jsonQuote encodes one string as a JSON string for the test request body.
func jsonQuote(value string) string {
	encodedValue, _ := json.Marshal(value)
	return string(encodedValue)
}

func TestServerServesEmbeddedMiniAppAndItsVersionedAssets(t *testing.T) {
	server := newTestServer(readinessCheckerFunc(func(applicationContext context.Context) error {
		return nil
	}))
	miniAppRequest := httptest.NewRequest(http.MethodGet, "/miniapp", nil)
	miniAppResponseRecorder := httptest.NewRecorder()

	server.ServeHTTP(miniAppResponseRecorder, miniAppRequest)

	if miniAppResponseRecorder.Code != http.StatusOK {
		t.Fatalf("GET /miniapp status = %d, want %d", miniAppResponseRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(miniAppResponseRecorder.Body.String(), "id=\"root\"") {
		t.Fatal("GET /miniapp did not return the compiled Mini App shell")
	}

	versionedAssetPath := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindStringSubmatch(miniAppResponseRecorder.Body.String())
	if len(versionedAssetPath) != 2 {
		t.Fatalf("GET /miniapp did not reference a versioned asset: %q", miniAppResponseRecorder.Body.String())
	}
	assetRequest := httptest.NewRequest(http.MethodGet, versionedAssetPath[1], nil)
	assetResponseRecorder := httptest.NewRecorder()

	server.ServeHTTP(assetResponseRecorder, assetRequest)

	if assetResponseRecorder.Code != http.StatusOK {
		t.Errorf("GET %s status = %d, want %d", versionedAssetPath[1], assetResponseRecorder.Code, http.StatusOK)
	}
}

func TestServerFallsBackOnlyWithinMiniAppRoutes(t *testing.T) {
	server := newTestServer(readinessCheckerFunc(func(applicationContext context.Context) error {
		return nil
	}))
	testCases := []struct {
		path       string
		wantStatus int
	}{
		{path: "/miniapp/future-route", wantStatus: http.StatusOK},
		{path: "/api/drafts", wantStatus: http.StatusNotFound},
	}

	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			responseRecorder := httptest.NewRecorder()

			server.ServeHTTP(responseRecorder, request)

			if responseRecorder.Code != testCase.wantStatus {
				t.Errorf("GET %s status = %d, want %d", testCase.path, responseRecorder.Code, testCase.wantStatus)
			}
		})
	}
}
