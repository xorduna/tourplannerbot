package webapp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"tourplannerbot/internal/buildinfo"
)

var testBuildInformation = buildinfo.Information{
	Version:   "main_abcdef0",
	BuildTime: "2026-09-20T10:30:00Z",
}

type readinessCheckerFunc func(applicationContext context.Context) error

// Check calls the function supplied by the test.
func (function readinessCheckerFunc) Check(applicationContext context.Context) error {
	return function(applicationContext)
}

// newTestServer creates an Echo server without writing request logs in test output.
func newTestServer(readinessChecker ReadinessChecker) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(logger, readinessChecker, testBuildInformation)
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
	}), testBuildInformation)
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
