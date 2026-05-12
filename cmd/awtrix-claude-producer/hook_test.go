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

func TestDispatchHook_UpsertEnrichesWithSourceColorAndCtxPct(t *testing.T) {
	h := newHookHarness(t)
	// Stage producer.env with G.3 fields
	cfgDir := filepath.Join(h.home, ".config", "awtrix-ai-status")
	env := "STATUS_SOURCE=test-mbp\nSTATUS_SERVER_URL=" + h.srv.URL + "\nSTATUS_TOKEN=tok\nSTATUS_SOURCE_COLOR=#aa66ff\nSTATUS_CONTEXT_PCT_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	// Stage transcript so computeContextPct returns ~20%
	sessionID := "test-session-xyz"
	tdir := filepath.Join(h.home, ".claude", "projects", "-test")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, sessionID+".jsonl"),
		[]byte(`{"type":"assistant","message":{"usage":{"input_tokens":40000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	in := hookInput{HookEventName: "UserPromptSubmit", SessionID: sessionID, CWD: "/repo", Prompt: "hi"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "user-prompt-submit", body)

	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"source_color":"#aa66ff"`) {
		t.Errorf("body missing source_color: %s", got)
	}
	if !strings.Contains(got, `"context_pct":20`) {
		t.Errorf("body missing context_pct=20: %s", got)
	}
}

func TestDispatchHook_ContextPctDisabledOmitsField(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "awtrix-ai-status")
	env := "STATUS_SOURCE=test-mbp\nSTATUS_SERVER_URL=" + h.srv.URL + "\nSTATUS_TOKEN=tok\nSTATUS_SOURCE_COLOR=#aa66ff\nSTATUS_CONTEXT_PCT_ENABLED=false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionID := "test-disabled"
	tdir := filepath.Join(h.home, ".claude", "projects", "-test")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, sessionID+".jsonl"),
		[]byte(`{"type":"assistant","message":{"usage":{"input_tokens":40000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	in := hookInput{HookEventName: "UserPromptSubmit", SessionID: sessionID, CWD: "/repo", Prompt: "hi"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "user-prompt-submit", body)

	got := (*h.bodies)[0]
	if strings.Contains(got, `"context_pct"`) {
		t.Errorf("context_pct should be omitted when disabled: %s", got)
	}
	// SourceColor is independent of ContextPctEnabled — should still ship.
	if !strings.Contains(got, `"source_color":"#aa66ff"`) {
		t.Errorf("source_color should still be present: %s", got)
	}
}

func TestDispatchHook_DeletePathUnchanged(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "awtrix-ai-status")
	env := "STATUS_SOURCE=test-mbp\nSTATUS_SERVER_URL=" + h.srv.URL + "\nSTATUS_TOKEN=tok\nSTATUS_SOURCE_COLOR=#aa66ff\nSTATUS_CONTEXT_PCT_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	// Create a marker so handleDelete has something to remove
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc.json"), []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "Stop", SessionID: "abc", CWD: "/repo"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "stop", body)

	if h.deletes.Load() != 1 {
		t.Fatalf("deletes = %d, want 1", h.deletes.Load())
	}
	got := (*h.bodies)[0]
	// DeleteRequest must NOT carry context_pct or source_color
	if strings.Contains(got, `"context_pct"`) || strings.Contains(got, `"source_color"`) {
		t.Errorf("DELETE body must not carry G.3 fields: %s", got)
	}
}
