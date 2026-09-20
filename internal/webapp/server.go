// Package webapp provides the HTTP endpoints served alongside the Telegram bot.
package webapp

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// ReadinessChecker reports whether dependencies required to serve application
// traffic are available.
type ReadinessChecker interface {
	Check(applicationContext context.Context) error
}

// NewServer creates the Echo server for the web application. It intentionally
// exposes only operational endpoints until the Mini App is added in a later slice.
func NewServer(logger *slog.Logger, readinessChecker ReadinessChecker) *echo.Echo {
	echoServer := echo.New()
	echoServer.Logger = logger
	echoServer.Use(middleware.Recover())
	echoServer.Use(middleware.RequestLogger())
	echoServer.GET("/healthz", handleLiveness)
	echoServer.GET("/readyz", handleReadiness(readinessChecker))
	return echoServer
}

// handleLiveness reports that the process is accepting HTTP requests without
// checking external dependencies.
func handleLiveness(echoContext *echo.Context) error {
	return echoContext.String(http.StatusOK, "ok\n")
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
