package webapp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type readinessCheckerFunc func(applicationContext context.Context) error

// Check calls the function supplied by the test.
func (function readinessCheckerFunc) Check(applicationContext context.Context) error {
	return function(applicationContext)
}

// newTestServer creates an Echo server without writing request logs in test output.
func newTestServer(readinessChecker ReadinessChecker) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(logger, readinessChecker)
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
	if responseRecorder.Body.String() != "ok\n" {
		t.Errorf("GET /healthz body = %q, want %q", responseRecorder.Body.String(), "ok\n")
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
	}))
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
