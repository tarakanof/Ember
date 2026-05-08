//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeServer mirrors the protocol-A server's session-store semantics enough
// to verify the producer's behavior end-to-end.
type fakeServer struct {
	mu       sync.Mutex
	sessions map[string]StatusRequest
	posts    int
	deletes  int
}

func newFakeServer() *fakeServer {
	return &fakeServer{sessions: map[string]StatusRequest{}}
}

func (f *fakeServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodPost:
			var req StatusRequest
			_ = json.Unmarshal(body, &req)
			f.sessions[req.Source+"/"+req.Tool+"/"+req.Session] = req
			f.posts++
			w.WriteHeader(200)
		case http.MethodDelete:
			var req DeleteRequest
			_ = json.Unmarshal(body, &req)
			delete(f.sessions, req.Source+"/"+req.Tool+"/"+req.Session)
			f.deletes++
			w.WriteHeader(204)
		}
	})
}

func TestIntegration_FullSession(t *testing.T) {
	fake := newFakeServer()
	srv := httptest.NewServer(fake.Handler())
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "awtrix-ai-status")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envContent := "STATUS_SOURCE=test\nSTATUS_SERVER_URL=" + srv.URL + "\nSTATUS_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()

	send := func(event string, in hookInput) {
		body, _ := json.Marshal(in)
		ctx, cancel := context.WithTimeout(context.Background(), 2e9)
		defer cancel()
		dispatchHook(ctx, event, body, cfg)
	}

	// SessionStart:startup is a no-op
	send("session-start", hookInput{HookEventName: "SessionStart", SessionID: "s1", CWD: "/r", Source: "startup"})
	if len(fake.sessions) != 0 {
		t.Errorf("after startup: expected 0 sessions, got %d", len(fake.sessions))
	}

	// UserPromptSubmit → running
	send("user-prompt-submit", hookInput{HookEventName: "UserPromptSubmit", SessionID: "s1", CWD: "/r", Prompt: "hi"})
	if len(fake.sessions) != 1 || fake.sessions["test/claude/s1"].State != "running" {
		t.Errorf("after prompt: expected 1 running session, got %v", fake.sessions)
	}

	// PreToolUse refreshes
	send("pre-tool-use", hookInput{HookEventName: "PreToolUse", SessionID: "s1", CWD: "/r", ToolName: "Bash"})
	if fake.sessions["test/claude/s1"].Message != "Bash" {
		t.Errorf("PreToolUse should set message=Bash; got %q", fake.sessions["test/claude/s1"].Message)
	}

	// Tick adds another POST without changing semantics
	dispatchTick(context.Background(), cfg)
	if fake.posts < 3 {
		t.Errorf("after tick: posts >= 3 expected, got %d", fake.posts)
	}

	// PermissionRequest → waiting
	send("permission-request", hookInput{HookEventName: "PermissionRequest", SessionID: "s1", CWD: "/r", ToolName: "Bash"})
	if fake.sessions["test/claude/s1"].State != "waiting" {
		t.Errorf("PermissionRequest should set state=waiting")
	}

	// Stop → DELETE
	send("stop", hookInput{HookEventName: "Stop", SessionID: "s1", CWD: "/r"})
	if len(fake.sessions) != 0 {
		t.Errorf("after Stop: expected 0 sessions, got %v", fake.sessions)
	}
	if fake.deletes != 1 {
		t.Errorf("deletes = %d, want 1", fake.deletes)
	}

	// Tick after Stop: no-op
	dispatchTick(context.Background(), cfg)
	if len(fake.sessions) != 0 {
		t.Errorf("tick after stop should not resurrect session, got %v", fake.sessions)
	}

	// SessionEnd:logout → DELETE (idempotent: session already gone)
	send("session-end", hookInput{HookEventName: "SessionEnd", SessionID: "s1", CWD: "/r", EndReason: "logout"})
	if len(fake.sessions) != 0 {
		t.Errorf("after logout: expected 0 sessions, got %v", fake.sessions)
	}
}
