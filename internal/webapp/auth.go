package webapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tourplannerbot/internal/models"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

const miniAppSessionCookieName = "tourplannerbot_miniapp_session"

var (
	// ErrInvalidTelegramInitData is returned when Telegram's signed launch data
	// is malformed, tampered with, or older than the configured maximum age.
	ErrInvalidTelegramInitData = errors.New("invalid Telegram Mini App init data")
	// ErrMiniAppAuthorizationUnavailable is returned until the database
	// connection needed to check allowed_users is ready.
	ErrMiniAppAuthorizationUnavailable = errors.New("Mini App authorization is unavailable")
	// ErrTelegramUserNotAllowed indicates that a valid Telegram user does not
	// have an allowed_users record.
	ErrTelegramUserNotAllowed = errors.New("Telegram user is not allowed")
)

// AllowedUserAuthorizer verifies whether a Telegram user is allowed to use the
// Mini App. Implementations must not grant access based only on a public ID.
type AllowedUserAuthorizer interface {
	IsAllowed(applicationContext context.Context, telegramUserID int64) (bool, error)
}

// SessionConfig contains the secret and expiry policy used by Mini App sessions.
type SessionConfig struct {
	TelegramBotToken     string
	AuthenticationMaxAge time.Duration
	Clock                func() time.Time
}

// TelegramUser is the minimal Telegram identity exposed to the Mini App.
type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// SessionAuthenticator validates Telegram launch data, authorizes users, and
// creates signed, short-lived session cookies for future API requests.
type SessionAuthenticator struct {
	telegramBotToken      string
	authenticationMaxAge  time.Duration
	allowedUserAuthorizer AllowedUserAuthorizer
	clock                 func() time.Time
}

// NewSessionAuthenticator creates the Mini App authentication service.
func NewSessionAuthenticator(sessionConfiguration SessionConfig, allowedUserAuthorizer AllowedUserAuthorizer) (*SessionAuthenticator, error) {
	if strings.TrimSpace(sessionConfiguration.TelegramBotToken) == "" {
		return nil, errors.New("Telegram bot token is required for Mini App authentication")
	}
	if sessionConfiguration.AuthenticationMaxAge <= 0 {
		return nil, errors.New("Mini App authentication maximum age must be positive")
	}
	if allowedUserAuthorizer == nil {
		return nil, errors.New("Mini App allowed-user authorizer is required")
	}
	if sessionConfiguration.Clock == nil {
		sessionConfiguration.Clock = time.Now
	}

	return &SessionAuthenticator{
		telegramBotToken:      strings.TrimSpace(sessionConfiguration.TelegramBotToken),
		authenticationMaxAge:  sessionConfiguration.AuthenticationMaxAge,
		allowedUserAuthorizer: allowedUserAuthorizer,
		clock:                 sessionConfiguration.Clock,
	}, nil
}

// GORMAllowedUserAuthorizer checks allowed_users after its connection is set by
// the application startup sequence. It keeps liveness available while the
// database reconnect loop is still running.
type GORMAllowedUserAuthorizer struct {
	databaseConnectionMutex sync.RWMutex
	databaseConnection      *gorm.DB
}

// NewGORMAllowedUserAuthorizer creates an authorizer without a database
// connection. SetDatabaseConnection must be called once PostgreSQL is ready.
func NewGORMAllowedUserAuthorizer() *GORMAllowedUserAuthorizer {
	return &GORMAllowedUserAuthorizer{}
}

// SetDatabaseConnection makes the supplied GORM connection available for
// subsequent authorization checks.
func (allowedUserAuthorizer *GORMAllowedUserAuthorizer) SetDatabaseConnection(databaseConnection *gorm.DB) {
	allowedUserAuthorizer.databaseConnectionMutex.Lock()
	defer allowedUserAuthorizer.databaseConnectionMutex.Unlock()
	allowedUserAuthorizer.databaseConnection = databaseConnection
}

// IsAllowed returns whether telegramUserID has a corresponding allowed_users row.
func (allowedUserAuthorizer *GORMAllowedUserAuthorizer) IsAllowed(applicationContext context.Context, telegramUserID int64) (bool, error) {
	allowedUserAuthorizer.databaseConnectionMutex.RLock()
	databaseConnection := allowedUserAuthorizer.databaseConnection
	allowedUserAuthorizer.databaseConnectionMutex.RUnlock()
	if databaseConnection == nil {
		return false, ErrMiniAppAuthorizationUnavailable
	}

	var authorizedUserCount int64
	if err := databaseConnection.WithContext(applicationContext).
		Model(&models.AllowedUser{}).
		Where("telegram_id = ?", telegramUserID).
		Count(&authorizedUserCount).
		Error; err != nil {
		return false, fmt.Errorf("query allowed Mini App user: %w", err)
	}
	return authorizedUserCount > 0, nil
}

type miniAppSessionRequest struct {
	InitData string `json:"init_data"`
}

type miniAppSessionResponse struct {
	User TelegramUser `json:"user"`
}

type signedMiniAppSession struct {
	TelegramUserID int64 `json:"telegram_user_id"`
	ExpiresAtUnix  int64 `json:"expires_at_unix"`
}

// registerMiniAppSessionRoutes adds the authenticated Mini App session
// handshake. The cookie is deliberately scoped to /api and never exposes its
// value to frontend JavaScript.
func registerMiniAppSessionRoutes(echoServer *echo.Echo, sessionAuthenticator *SessionAuthenticator) {
	echoServer.POST("/api/miniapp/session", sessionAuthenticator.handleSessionHandshake)
}

// handleSessionHandshake validates Telegram initData, checks allowed_users,
// writes a signed cookie, and returns the authenticated Telegram identity.
func (sessionAuthenticator *SessionAuthenticator) handleSessionHandshake(echoContext *echo.Context) error {
	request := echoContext.Request()
	request.Body = http.MaxBytesReader(echoContext.Response(), request.Body, 16*1024)
	var sessionRequest miniAppSessionRequest
	if err := json.NewDecoder(request.Body).Decode(&sessionRequest); err != nil {
		return echoContext.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid Telegram credentials"})
	}

	telegramUser, err := sessionAuthenticator.authenticateInitData(request.Context(), sessionRequest.InitData)
	if errors.Is(err, ErrInvalidTelegramInitData) {
		return echoContext.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid Telegram credentials"})
	}
	if errors.Is(err, ErrMiniAppAuthorizationUnavailable) {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "authorization temporarily unavailable"})
	}
	if errors.Is(err, ErrTelegramUserNotAllowed) {
		return echoContext.JSON(http.StatusForbidden, map[string]string{"error": "Telegram user is not allowed"})
	}
	if err != nil {
		return echoContext.JSON(http.StatusServiceUnavailable, map[string]string{"error": "authorization temporarily unavailable"})
	}

	if err := sessionAuthenticator.setSessionCookie(echoContext.Response(), telegramUser.ID); err != nil {
		return fmt.Errorf("create Mini App session: %w", err)
	}
	return echoContext.JSON(http.StatusOK, miniAppSessionResponse{User: telegramUser})
}

// authenticateInitData validates the Telegram signature and age before asking
// the configured authorizer whether the resulting user may use this Mini App.
func (sessionAuthenticator *SessionAuthenticator) authenticateInitData(applicationContext context.Context, rawInitData string) (TelegramUser, error) {
	telegramUser, authenticatedAt, err := sessionAuthenticator.validateTelegramInitData(rawInitData)
	if err != nil {
		return TelegramUser{}, err
	}
	currentTime := sessionAuthenticator.clock()
	if currentTime.Sub(authenticatedAt) > sessionAuthenticator.authenticationMaxAge || authenticatedAt.After(currentTime.Add(30*time.Second)) {
		return TelegramUser{}, ErrInvalidTelegramInitData
	}

	isAllowed, err := sessionAuthenticator.allowedUserAuthorizer.IsAllowed(applicationContext, telegramUser.ID)
	if err != nil {
		return TelegramUser{}, err
	}
	if !isAllowed {
		return TelegramUser{}, ErrTelegramUserNotAllowed
	}
	return telegramUser, nil
}

// validateTelegramInitData implements Telegram's Web App data-check-string
// verification. It intentionally uses only the received, URL-decoded values.
func (sessionAuthenticator *SessionAuthenticator) validateTelegramInitData(rawInitData string) (TelegramUser, time.Time, error) {
	parsedValues, err := url.ParseQuery(rawInitData)
	if err != nil || len(parsedValues) == 0 {
		return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
	}
	for parameterName, parameterValues := range parsedValues {
		if len(parameterValues) != 1 || parameterName == "" {
			return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
		}
	}

	receivedHash := parsedValues.Get("hash")
	userJSON := parsedValues.Get("user")
	authDateString := parsedValues.Get("auth_date")
	if receivedHash == "" || userJSON == "" || authDateString == "" {
		return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
	}
	receivedHashBytes, err := hex.DecodeString(receivedHash)
	if err != nil || len(receivedHashBytes) != sha256.Size {
		return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
	}

	dataCheckValues := make([]string, 0, len(parsedValues)-1)
	for parameterName, parameterValues := range parsedValues {
		if parameterName != "hash" {
			dataCheckValues = append(dataCheckValues, parameterName+"="+parameterValues[0])
		}
	}
	sort.Strings(dataCheckValues)

	secretKeyMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretKeyMAC.Write([]byte(sessionAuthenticator.telegramBotToken))
	dataCheckMAC := hmac.New(sha256.New, secretKeyMAC.Sum(nil))
	_, _ = dataCheckMAC.Write([]byte(strings.Join(dataCheckValues, "\n")))
	if subtle.ConstantTimeCompare(receivedHashBytes, dataCheckMAC.Sum(nil)) != 1 {
		return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
	}

	authDateUnix, err := strconv.ParseInt(authDateString, 10, 64)
	if err != nil || authDateUnix <= 0 {
		return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
	}
	var telegramUser TelegramUser
	if err := json.Unmarshal([]byte(userJSON), &telegramUser); err != nil || telegramUser.ID <= 0 {
		return TelegramUser{}, time.Time{}, ErrInvalidTelegramInitData
	}
	return telegramUser, time.Unix(authDateUnix, 0), nil
}

// setSessionCookie encodes and signs a short-lived API session.
func (sessionAuthenticator *SessionAuthenticator) setSessionCookie(responseWriter http.ResponseWriter, telegramUserID int64) error {
	expiresAt := sessionAuthenticator.clock().Add(sessionAuthenticator.authenticationMaxAge)
	sessionBytes, err := json.Marshal(signedMiniAppSession{TelegramUserID: telegramUserID, ExpiresAtUnix: expiresAt.Unix()})
	if err != nil {
		return err
	}
	payload := base64.RawURLEncoding.EncodeToString(sessionBytes)
	signature := sessionAuthenticator.signSessionPayload(payload)
	http.SetCookie(responseWriter, &http.Cookie{
		Name:     miniAppSessionCookieName,
		Value:    payload + "." + signature,
		Path:     "/api",
		Expires:  expiresAt,
		MaxAge:   int(sessionAuthenticator.authenticationMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// AuthenticateSessionRequest verifies a signed API session and confirms that
// its Telegram user remains authorized. Future draft endpoints must call this
// before loading or modifying any data.
func (sessionAuthenticator *SessionAuthenticator) AuthenticateSessionRequest(request *http.Request) (int64, error) {
	sessionCookie, err := request.Cookie(miniAppSessionCookieName)
	if err != nil {
		return 0, ErrInvalidTelegramInitData
	}
	payload, encodedSignature, foundSignature := strings.Cut(sessionCookie.Value, ".")
	if !foundSignature || payload == "" || encodedSignature == "" || strings.Contains(encodedSignature, ".") {
		return 0, ErrInvalidTelegramInitData
	}
	if subtle.ConstantTimeCompare([]byte(encodedSignature), []byte(sessionAuthenticator.signSessionPayload(payload))) != 1 {
		return 0, ErrInvalidTelegramInitData
	}
	sessionBytes, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return 0, ErrInvalidTelegramInitData
	}
	var session signedMiniAppSession
	if err := json.Unmarshal(sessionBytes, &session); err != nil || session.TelegramUserID <= 0 || session.ExpiresAtUnix <= sessionAuthenticator.clock().Unix() {
		return 0, ErrInvalidTelegramInitData
	}
	isAllowed, err := sessionAuthenticator.allowedUserAuthorizer.IsAllowed(request.Context(), session.TelegramUserID)
	if err != nil {
		return 0, err
	}
	if !isAllowed {
		return 0, ErrTelegramUserNotAllowed
	}
	return session.TelegramUserID, nil
}

// signSessionPayload signs cookie data with a key derived from the bot token.
func (sessionAuthenticator *SessionAuthenticator) signSessionPayload(payload string) string {
	signingKey := sha256.Sum256([]byte("tourplannerbot-miniapp-session:" + sessionAuthenticator.telegramBotToken))
	signatureMAC := hmac.New(sha256.New, signingKey[:])
	_, _ = signatureMAC.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(signatureMAC.Sum(nil))
}
