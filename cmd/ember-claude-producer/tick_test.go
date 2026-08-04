package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
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

// TestTick_CodexMarker_SkippedEntirely is the Task-7 regression test: the
// Claude daemon shares the marker dir with the Codex producer. A codex
// marker — fresh or stale — must never be POSTed, reaped/DELETEd, or
// rewritten by the Claude daemon; the Codex daemon owns its own markers'
// full lifecycle.
func TestTick_CodexMarker_SkippedEntirely(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "codex-sess.json")
	body := []byte(`{"source":"test-mbp","tool":"codex","session":"codex-sess","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// Make it stale too, so we also exercise the reap path.
	old := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(markerP, old, old); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 0 {
		t.Errorf("codex marker must not be POSTed by the claude daemon; posts=%d", h.posts.Load())
	}
	if h.deletes.Load() != 0 {
		t.Errorf("codex marker must not be DELETEd/reaped by the claude daemon; deletes=%d", h.deletes.Load())
	}
	after, err := os.ReadFile(markerP)
	if err != nil {
		t.Fatalf("codex marker must not be removed by the claude daemon: %v", err)
	}
	if string(after) != string(body) {
		t.Errorf("codex marker content must be left untouched, got: %s", after)
	}
}

// TestTick_LegacyMarkerNoToolField_TreatedAsClaude verifies the documented
// backward-compat decision: a marker predating the "tool" field (missing or
// empty) is treated as a claude marker, since both current producers always
// write an explicit "tool" value — an empty Tool can only mean a pre-upgrade
// marker written by the (older) Claude producer.
func TestTick_LegacyMarkerNoToolField_TreatedAsClaude(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "legacy-sess.json")
	body := []byte(`{"source":"test-mbp","session":"legacy-sess","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 {
		t.Errorf("legacy no-tool marker should be treated as claude and re-posted; posts=%d", h.posts.Load())
	}
}

// TestDispatchTick_WarnsOnPostFailure is the Task-9 error-visibility
// regression test: a failing status POST must produce a throttled slog Warn
// instead of being silently discarded (previously `_ = client.Post(...)`).
func TestDispatchTick_WarnsOnPostFailure(t *testing.T) {
	tickFailLog.Reset()
	h := newHookHarness(t)
	h.srv.Close()
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer h.srv.Close()
	cfgDir := filepath.Join(h.home, ".config", "ember")
	envContent := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if !strings.Contains(buf.String(), "kind=claude_post") {
		t.Errorf("expected a throttled claude_post warning, got: %s", buf.String())
	}
}

// TestDispatchTick_ThrottlesRepeatedPostFailures confirms a second failing
// tick within the throttle period does not log a second warning.
func TestDispatchTick_ThrottlesRepeatedPostFailures(t *testing.T) {
	tickFailLog.Reset()
	h := newHookHarness(t)
	h.srv.Close()
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer h.srv.Close()
	cfgDir := filepath.Join(h.home, ".config", "ember")
	envContent := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	body := []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	dispatchTick(context.Background(), cfg)
	if n := strings.Count(buf.String(), "kind=claude_post"); n != 1 {
		t.Errorf("expected exactly 1 throttled warning across 2 failing ticks, got %d: %s", n, buf.String())
	}
}

// usageRelayHarness is like newHookHarness but splits captured request bodies
// by path, since dispatchTick now also POSTs to /v1/usage alongside /v1/status.
type usageRelayHarness struct {
	home         string
	srv          *httptest.Server
	statusBodies []string
	usageBodies  []string
}

func newUsageRelayHarness(t *testing.T) *usageRelayHarness {
	t.Helper()
	h := &usageRelayHarness{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.statusBodies = append(h.statusBodies, string(b))
		w.WriteHeader(204)
	})
	mux.HandleFunc("/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.usageBodies = append(h.usageBodies, string(b))
		w.WriteHeader(204)
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	h.home = t.TempDir()
	t.Setenv("HOME", h.home)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *usageRelayHarness) sessionsDir() string {
	return filepath.Join(h.home, ".local", "state", "ember", "sessions")
}

func writeMarkerFile(t *testing.T, dir, sessionID, extraJSONFields string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sessionID+".json")
	body := `{"source":"test-mbp","tool":"claude","session":"` + sessionID + `","state":"running"` + extraJSONFields + `}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDispatchTick_NoRateData_NoUsagePost(t *testing.T) {
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeMarkerFile(t, h.sessionsDir(), "abc", "")
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if len(h.statusBodies) != 1 {
		t.Fatalf("status posts = %d, want 1", len(h.statusBodies))
	}
	if len(h.usageBodies) != 0 {
		t.Errorf("usage posts = %d, want 0 (no rate data on marker)", len(h.usageBodies))
	}
}

func TestDispatchTick_RelaysWeeklyUsageFromStatusline(t *testing.T) {
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeMarkerFile(t, h.sessionsDir(), "abc",
		`,"rate_week_pct":42,"rate_week_reset_at":1778700000,"rate_week_reset_label":"MON"`)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if len(h.usageBodies) != 1 {
		t.Fatalf("usage posts = %d, want 1", len(h.usageBodies))
	}
	var req struct {
		Tool     string          `json:"tool"`
		Source   string          `json:"source"`
		FiveHour *producerWindow `json:"five_hour"`
		SevenDay *producerWindow `json:"seven_day"`
	}
	if err := json.Unmarshal([]byte(h.usageBodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.Tool != "claude" || req.Source != "statusline" {
		t.Errorf("tool/source = %q/%q, want claude/statusline", req.Tool, req.Source)
	}
	if req.FiveHour != nil {
		t.Errorf("five_hour should be absent, got %+v", req.FiveHour)
	}
	if req.SevenDay == nil || req.SevenDay.UsedPercent != 42 || req.SevenDay.ResetsAt != 1778700000 || req.SevenDay.ResetLabel != "MON" {
		t.Errorf("seven_day = %+v, want {42 1778700000 MON}", req.SevenDay)
	}
}

func TestDispatchTick_RelaysFiveHourAndWeeklyTogether(t *testing.T) {
	usageModels.reset()
	h := newUsageRelayHarness(t)
	writeMarkerFile(t, h.sessionsDir(), "abc",
		`,"rate_window_pct":73,"rate_reset_at":1778614633,"rate_reset_label":"14:25",`+
			`"rate_week_pct":42,"rate_week_reset_at":1778700000,"rate_week_reset_label":"MON"`)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if len(h.usageBodies) != 1 {
		t.Fatalf("usage posts = %d, want 1", len(h.usageBodies))
	}
	var req struct {
		FiveHour *producerWindow `json:"five_hour"`
		SevenDay *producerWindow `json:"seven_day"`
	}
	if err := json.Unmarshal([]byte(h.usageBodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.FiveHour == nil || req.FiveHour.UsedPercent != 73 {
		t.Errorf("five_hour = %+v, want UsedPercent 73", req.FiveHour)
	}
	if req.SevenDay == nil || req.SevenDay.UsedPercent != 42 {
		t.Errorf("seven_day = %+v, want UsedPercent 42", req.SevenDay)
	}
}

// producerWindow mirrors producer.UsageWindow's wire shape for test decoding.
type producerWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at"`
	ResetLabel  string  `json:"reset_label"`
}

func TestDispatchTick_PicksFreshestMarkerAcrossSessions(t *testing.T) {
	usageModels.reset()
	h := newUsageRelayHarness(t)
	dir := h.sessionsDir()
	pOld := writeMarkerFile(t, dir, "old", `,"rate_week_pct":10,"rate_week_reset_at":1000,"rate_week_reset_label":"MON"`)
	pNew := writeMarkerFile(t, dir, "new", `,"rate_week_pct":90,"rate_week_reset_at":2000,"rate_week_reset_label":"TUE"`)
	older := time.Now().Add(-time.Minute)
	if err := os.Chtimes(pOld, older, older); err != nil {
		t.Fatal(err)
	}
	newer := time.Now()
	if err := os.Chtimes(pNew, newer, newer); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if len(h.usageBodies) != 1 {
		t.Fatalf("usage posts = %d, want 1", len(h.usageBodies))
	}
	var req struct {
		SevenDay *producerWindow `json:"seven_day"`
	}
	if err := json.Unmarshal([]byte(h.usageBodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.SevenDay == nil || req.SevenDay.UsedPercent != 90 {
		t.Errorf("expected the freshest (newer) marker's seven_day=90, got %+v", req.SevenDay)
	}
}

// TestDispatchTick_UsagePost_MergesModelsCache is the key precedence
// regression test: Claude Code's statusline JSON carries no per-model
// breakdown, so the statusline-driven /v1/usage POST must carry forward the
// last per-model snapshot the OAuth poller cached — otherwise the server's
// last-write-wins UsageStore.Put would blank the per-model breakdown on every
// heartbeat between OAuth-endpoint polls.
func TestDispatchTick_UsagePost_MergesModelsCache(t *testing.T) {
	usageModels.reset()
	t.Cleanup(usageModels.reset)
	usageModels.set(map[string]*producer.UsageWindow{
		"opus":   {UsedPercent: 82},
		"sonnet": {UsedPercent: 12},
	})
	h := newUsageRelayHarness(t)
	writeMarkerFile(t, h.sessionsDir(), "abc",
		`,"rate_week_pct":42,"rate_week_reset_at":1778700000,"rate_week_reset_label":"MON"`)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if len(h.usageBodies) != 1 {
		t.Fatalf("usage posts = %d, want 1", len(h.usageBodies))
	}
	var req struct {
		Models map[string]*producerWindow `json:"models"`
	}
	if err := json.Unmarshal([]byte(h.usageBodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.Models["opus"] == nil || req.Models["opus"].UsedPercent != 82 {
		t.Errorf("models[opus] = %+v, want UsedPercent 82", req.Models["opus"])
	}
	if req.Models["sonnet"] == nil || req.Models["sonnet"].UsedPercent != 12 {
		t.Errorf("models[sonnet] = %+v, want UsedPercent 12", req.Models["sonnet"])
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
