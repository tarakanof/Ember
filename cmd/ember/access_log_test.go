package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestLoggingMiddlewareLevelAndStatus asserts the access log records the
// response status and keeps every successful request (menu polling, producer
// heartbeats) at Debug, while failures stay at Info.
func TestLoggingMiddlewareLevelAndStatus(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		status    int
		wantLevel string
	}{
		{"successful get", http.MethodGet, http.StatusOK, "level=DEBUG"},
		{"not modified get", http.MethodGet, http.StatusNotModified, "level=DEBUG"},
		{"failed get", http.MethodGet, http.StatusNotFound, "level=INFO"},
		{"successful post", http.MethodPost, http.StatusOK, "level=DEBUG"},
		{"no content delete", http.MethodDelete, http.StatusNoContent, "level=DEBUG"},
		{"rejected put", http.MethodPut, http.StatusUnauthorized, "level=INFO"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			h := loggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, "/state", nil))

			line := buf.String()
			if !strings.Contains(line, c.wantLevel) {
				t.Errorf("log line %q: want %s", line, c.wantLevel)
			}
			if !strings.Contains(line, "status="+strconv.Itoa(c.status)) {
				t.Errorf("log line %q: missing status=%d", line, c.status)
			}
		})
	}
}
