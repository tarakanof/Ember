package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTick_NoMarkers_NoOp(t *testing.T) {
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("empty state dir should produce no traffic; posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
}

func TestTick_FreshMarker_RePosts(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running","message":"Bash"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 {
		t.Errorf("fresh marker should produce one POST; got %d", h.posts.Load())
	}
}

func TestTick_StaleMarker_RemovedAndDeleted(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(markerP, old, old); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.deletes.Load() != 1 {
		t.Errorf("stale marker should produce one DELETE; got %d", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("stale marker should be removed")
	}
}

// TestTick_NoResurrectionUnderConcurrentStop is the canonical Ghost Heartbeat
// acceptance test: a tick is enumerating a marker concurrently with a Stop
// hook deleting it. Lock-based ordering must guarantee no POST is observed
// after the DELETE.
func TestTick_NoResurrectionUnderConcurrentStop(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running","message":"Bash"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _ := loadConfig()
	var sawPostAfterDelete atomic.Bool
	var deleteSeen atomic.Bool

	// Replace harness handler with one that detects POST-after-DELETE
	h.srv.Close()
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deleteSeen.Store(true)
			h.deletes.Add(1)
		case http.MethodPost:
			h.posts.Add(1)
			if deleteSeen.Load() {
				sawPostAfterDelete.Store(true)
			}
		}
		w.WriteHeader(204)
	}))
	defer h.srv.Close()
	cfgDir := filepath.Join(h.home, ".config", "ember")
	envContent := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ = loadConfig()

	// Concurrent goroutines: one running tick, one running Stop hook.
	// Repeat several times to amplify any race window. The Ghost Heartbeat
	// invariant is *per iteration* (a POST after Stop's DELETE in the same
	// logical session lifetime), so deleteSeen is reset at the top of every
	// iteration. Without the reset, a legitimate POST in iteration N would
	// be flagged as a ghost just because Stop ran in iteration N-1.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		deleteSeen.Store(false)

		// Re-create marker for each iteration (Stop deletes it)
		if err := os.WriteFile(markerP, body, 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			dispatchTick(context.Background(), cfg)
		}()
		go func() {
			defer wg.Done()
			in := hookInput{HookEventName: "Stop", SessionID: "abc", CWD: "/repo"}
			b, _ := json.Marshal(in)
			dispatchHook(context.Background(), "stop", b, cfg)
		}()
		wg.Wait()

		if sawPostAfterDelete.Load() {
			t.Fatalf("Ghost Heartbeat: POST observed after DELETE in iteration %d", i)
		}
	}
}

// heartbeatPass is the daemon's per-iteration body: it reloads config (so
// producer.env edits apply without a restart) and runs one tick.
func TestHeartbeatPass_RePostsMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	heartbeatPass(context.Background())
	if h.posts.Load() != 1 {
		t.Errorf("heartbeatPass should re-post the fresh marker; posts=%d", h.posts.Load())
	}
}

func TestHeartbeatPass_NoConfig_NoOp(t *testing.T) {
	h := newHookHarness(t)
	// Blank out the config so Source/ServerURL are empty.
	cfgPath := filepath.Join(h.home, ".config", "ember", "producer.env")
	if err := os.WriteFile(cfgPath, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc.json"),
		[]byte(`{"source":"x","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	heartbeatPass(context.Background())
	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("heartbeatPass with empty config must do nothing; posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
}

func TestProcessOneMarker_PreservesContextPctWhenEnabled(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_CONTEXT_PCT_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionID := "tick-ctx-session"
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, sessionID+".json")
	if err := os.WriteFile(markerP,
		[]byte(`{"source":"test-mbp","tool":"claude","session":"`+sessionID+`","state":"running","context_pct":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"context_pct":42`) {
		t.Errorf("tick should re-post stored context_pct=42 unchanged, got: %s", got)
	}
}

func TestProcessOneMarker_StripsContextPctWhenDisabled(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_CONTEXT_PCT_ENABLED=false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionID := "tick-ctx-off"
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, sessionID+".json")
	if err := os.WriteFile(markerP,
		[]byte(`{"source":"test-mbp","tool":"claude","session":"`+sessionID+`","state":"running","context_pct":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	got := (*h.bodies)[0]
	if strings.Contains(got, `"context_pct"`) {
		t.Errorf("disabled: tick should strip context_pct from re-post, got: %s", got)
	}
}

func TestProcessOneMarker_ReGatesSourceCardAndSessionBarWhenDisabled(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	// Config has both toggles disabled; marker was written when they were enabled.
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_SOURCE_CARD=false\nEMBER_SESSION_BAR=false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionID := "tick-sc-sb-off"
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, sessionID+".json")
	// Marker carries source_card:true and session_bar:true (written when enabled).
	if err := os.WriteFile(markerP,
		[]byte(`{"source":"test-mbp","tool":"claude","session":"`+sessionID+`","state":"running","source_card":true,"session_bar":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	got := (*h.bodies)[0]
	var posted StatusRequest
	if err := json.Unmarshal([]byte(got), &posted); err != nil {
		t.Fatalf("unmarshal posted body: %v", err)
	}
	if posted.SourceCard == nil || *posted.SourceCard {
		t.Errorf("re-gate: source_card should be false in re-post, got %v", posted.SourceCard)
	}
	if posted.SessionBar == nil || *posted.SessionBar {
		t.Errorf("re-gate: session_bar should be false in re-post, got %v", posted.SessionBar)
	}
}
