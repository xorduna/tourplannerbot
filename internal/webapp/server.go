// Package webapp provides the HTTP endpoints served alongside the Telegram bot.
package webapp

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"

	"tourplannerbot/internal/buildinfo"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// ReadinessChecker reports whether dependencies required to serve application
// traffic are available.
type ReadinessChecker interface {
	Check(applicationContext context.Context) error
}

type livenessResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
}

// NewServer creates the Echo server for the operational endpoints and embedded Mini App.
func NewServer(logger *slog.Logger, readinessChecker ReadinessChecker, buildInformation buildinfo.Information, sessionAuthenticator *SessionAuthenticator, draftReader DraftReader) *echo.Echo {
	echoServer := echo.New()
	echoServer.Logger = logger
	echoServer.Use(middleware.Recover())
	echoServer.Use(middleware.RequestLogger())
	echoServer.GET("/healthz", handleLiveness(buildInformation))
	echoServer.GET("/readyz", handleReadiness(readinessChecker))
	if sessionAuthenticator != nil {
		registerMiniAppSessionRoutes(echoServer, sessionAuthenticator)
	}
	if sessionAuthenticator != nil && draftReader != nil {
		registerDraftRoutes(echoServer, sessionAuthenticator, draftReader)
	}
	registerMiniAppRoutes(echoServer, embeddedAssetFileSystem())
	return echoServer
}

// registerMiniAppRoutes serves Vite's versioned assets and limits the SPA
// fallback to /miniapp so unknown API routes retain their normal 404 response.
func registerMiniAppRoutes(echoServer *echo.Echo, assetFileSystem fs.FS) {
	echoServer.StaticFS("/assets/", echo.MustSubFS(assetFileSystem, "assets"))
	echoServer.FileFS("/miniapp", "index.html", assetFileSystem)
	echoServer.GET("/miniapp/*", func(echoContext *echo.Context) error {
		return echoContext.FileFS("index.html", assetFileSystem)
	})
}

// handleLiveness reports that the process is accepting HTTP requests without
// checking external dependencies.
func handleLiveness(buildInformation buildinfo.Information) echo.HandlerFunc {
	return func(echoContext *echo.Context) error {
		return echoContext.JSON(http.StatusOK, livenessResponse{
			Status:    "ok",
			Version:   buildInformation.Version,
			BuildTime: buildInformation.BuildTime,
		})
	}
}

// handleReadiness verifies that PostgreSQL can accept a request.
func handleReadiness(readinessChecker ReadinessChecker) echo.HandlerFunc {
	return func(echoContext *echo.Context) error {
		if err := readinessChecker.Check(echoContext.Request().Context()); err != nil {
			return echoContext.String(http.StatusServiceUnavailable, "database unavailable\n")
		}

		return echoContext.String(http.StatusOK, "ok\n")
	}
}
