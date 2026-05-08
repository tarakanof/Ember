package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// hookHarness builds a temp HOME, a fake server, and minimal config.
type hookHarness struct {
	t       *testing.T
	home    string
	srv     *httptest.Server
	posts   *atomic.Int32
	deletes *atomic.Int32
	bodies  *[]string
}

func newHookHarness(t *testing.T) *hookHarness {
	t.Helper()
	posts := &atomic.Int32{}
	deletes := &atomic.Int32{}
	bodies := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(b))
		switch r.Method {
		case http.MethodPost:
			posts.Add(1)
		case http.MethodDelete:
			deletes.Add(1)
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envContent := "STATUS_SOURCE=test-mbp\nSTATUS_SERVER_URL=" + srv.URL + "\nSTATUS_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return &hookHarness{t: t, home: home, srv: srv, posts: posts, deletes: deletes, bodies: bodies}
}

func (h *hookHarness) sessionsDir() string {
	return filepath.Join(h.home, ".local", "state", "awtrix-ai-status", "sessions")
}

func TestHook_UserPromptSubmit_UpsertsRunning(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{
		HookEventName: "UserPromptSubmit",
		SessionID:     "abc",
		CWD:           "/repo",
		Prompt:        "fix the bug",
	}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "user-prompt-submit", body)
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1", h.posts.Load())
	}
	if !strings.Contains((*h.bodies)[0], `"state":"running"`) {
		t.Errorf("body missing state=running: %q", (*h.bodies)[0])
	}
	if !strings.Contains((*h.bodies)[0], `"message":"fix the bug"`) {
		t.Errorf("body missing prompt as message: %q", (*h.bodies)[0])
	}
	if _, err := os.Stat(filepath.Join(h.sessionsDir(), "abc.json")); err != nil {
		t.Errorf("marker file missing: %v", err)
	}
}

func TestHook_Stop_DeletesAndRemovesMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "Stop", SessionID: "abc", CWD: "/repo"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "stop", body)
	if h.deletes.Load() != 1 {
		t.Errorf("deletes = %d, want 1", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("marker should be removed after stop")
	}
}

func TestHook_PermissionRequest_UpsertsWaiting(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "PermissionRequest", SessionID: "abc", CWD: "/repo", ToolName: "Bash"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "permission-request", body)
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1", h.posts.Load())
	}
	if !strings.Contains((*h.bodies)[0], `"state":"waiting"`) {
		t.Errorf("body missing state=waiting: %q", (*h.bodies)[0])
	}
	if !strings.Contains((*h.bodies)[0], `"message":"approve Bash"`) {
		t.Errorf("body missing approve <Bash> message: %q", (*h.bodies)[0])
	}
}

func TestHook_Notification_FiltersByType(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "Notification", SessionID: "abc", CWD: "/repo",
		NotificationType: "idle_prompt", NotificationMessage: "just chilling"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "notification", body)
	if h.posts.Load() != 0 {
		t.Errorf("idle_prompt should not POST; posts = %d", h.posts.Load())
	}
}

func TestHook_SessionStart_NonStartupClearsMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "SessionStart", SessionID: "abc", CWD: "/repo", Source: "resume"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "session-start", body)
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("non-startup SessionStart should clear pre-existing marker")
	}
}

// dispatchHookForTest is the testable seam: calls dispatchHook with a context+timeout.
func dispatchHookForTest(t *testing.T, event string, stdin []byte) {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	dispatchHook(context.Background(), event, stdin, cfg)
}
