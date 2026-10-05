package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// SessionEnd landing while the heartbeat POST is in flight must not leave a
// ghost session: the last request the server sees is a DELETE, also when the
// POST errors (a timed-out POST may still have been applied).
func TestTick_NoResurrectionWhenSessionEndsDuringPost(t *testing.T) {
	for _, status := range []int{204, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := newHookHarness(t)
			dir := h.sessionsDir()
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			markerP := filepath.Join(dir, "abc.json")
			if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, _ := loadConfig()
			var mu sync.Mutex
			var reqs []string
			h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				reqs = append(reqs, r.Method)
				first := len(reqs) == 1
				mu.Unlock()
				if first {
					dispatchHook(context.Background(), "session-end", []byte(`{"session_id":"abc","cwd":"/repo","reason":"other"}`), cfg)
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(204)
			})
			dispatchTick(context.Background(), cfg)
			mu.Lock()
			defer mu.Unlock()
			if len(reqs) < 3 || reqs[len(reqs)-1] != http.MethodDelete {
				t.Errorf("requests = %v, want POST, DELETE (SessionEnd), DELETE (reconcile)", reqs)
			}
		})
	}
}

func TestHeartbeatDue_DoneAndErrorExpire(t *testing.T) {
	cfg := Config{DoneTTLSeconds: 30}
	now := time.Unix(1_800_000_000, 0)
	at := func(ago time.Duration) int64 { return now.Add(-ago).Unix() }
	cases := []struct {
		name  string
		m     marker
		mtime time.Duration
		want  bool
	}{
		{"fresh done", marker{StatusRequest: StatusRequest{State: "done"}, StateChangedAt: at(10 * time.Second)}, 0, true},
		{"expired done", marker{StatusRequest: StatusRequest{State: "done"}, StateChangedAt: at(31 * time.Second)}, 0, false},
		{"expired error", marker{StatusRequest: StatusRequest{State: "error"}, StateChangedAt: at(time.Minute)}, 0, false},
		{"old running", marker{StatusRequest: StatusRequest{State: "running"}, StateChangedAt: at(time.Hour)}, 0, true},
		{"old waiting", marker{StatusRequest: StatusRequest{State: "waiting"}, StateChangedAt: at(time.Hour)}, 0, true},
		{"legacy done, old mtime", marker{StatusRequest: StatusRequest{State: "done"}}, time.Minute, false},
		{"legacy done, fresh mtime", marker{StatusRequest: StatusRequest{State: "done"}}, time.Second, true},
	}
	for _, c := range cases {
		if got := heartbeatDue(cfg, c.m, now.Add(-c.mtime), now); got != c.want {
			t.Errorf("%s: heartbeatDue = %v, want %v", c.name, got, c.want)
		}
	}
}

// An expired done marker stays on disk (local state) but isn't re-posted, so
// the server's done linger runs out and the idle screen returns.
func TestTick_ExpiredDoneNotReposted(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute).Unix()
	markerP := filepath.Join(dir, "abc.json")
	body := `{"source":"test-mbp","tool":"claude","session":"abc","state":"done","state_changed_at":` + strconv.FormatInt(old, 10) + `}`
	if err := os.WriteFile(markerP, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("posts=%d deletes=%d, want none for an expired done", h.posts.Load(), h.deletes.Load())
	}
	if _, err := os.Stat(markerP); err != nil {
		t.Errorf("marker should be kept: %v", err)
	}
}

// The daemon must not hold the session lock across its POST (#258), or every
// hook stalls for the daemon's HTTP timeout when the server is unreachable.
func TestProcessOneMarker_PostsOutsideLock(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	lockP := filepath.Join(dir, "abc.lock")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var lockFree atomic.Bool
	var calls atomic.Int32
	h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			lockFree.Store(withLockExWait(lockP, 100*time.Millisecond, func() error { return nil }) == nil)
		}
		w.WriteHeader(204)
	})
	cfg, _ := loadConfig()
	processOneMarker(context.Background(), cfg, NewDaemonClient(cfg), markerP, lockP, time.Now().Add(-time.Hour))
	if !lockFree.Load() {
		t.Error("session lock was held while the heartbeat POST was in flight")
	}
}

// A hook that changes the marker while the heartbeat POST is in flight must
// win: the daemon re-sends the newer marker rather than leave its stale copy.
func TestProcessOneMarker_ResendsMarkerChangedDuringPost(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	lockP := filepath.Join(dir, "abc.lock")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var states []string
	var mu sync.Mutex
	h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req StatusRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		states = append(states, r.Method+" "+req.State)
		first := len(states) == 1
		mu.Unlock()
		if first {
			_ = os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"done"}`), 0o600)
		}
		w.WriteHeader(204)
	})
	cfg, _ := loadConfig()
	processOneMarker(context.Background(), cfg, NewDaemonClient(cfg), markerP, lockP, time.Now().Add(-time.Hour))
	mu.Lock()
	defer mu.Unlock()
	if len(states) != 2 || states[1] != "POST done" {
		t.Errorf("requests = %v, want [POST running, POST done]", states)
	}
}

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

func TestDispatchTick_WarnsOnPostFailure(t *testing.T) {
	tickFailLog = producer.NewFailureLogger(time.Minute)
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

func TestDispatchTick_ThrottlesRepeatedPostFailures(t *testing.T) {
	tickFailLog = producer.NewFailureLogger(time.Minute)
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

func TestDispatchTick_FreshestByStatuslineChange(t *testing.T) {
	cases := []struct {
		name               string
		changedA, changedB int64 // statusline_changed_ms
		mtimeANewer        bool
		want               float64
	}{
		// A's figures changed most recently, though B was written later.
		{"newest change wins over mtime", 2_000_500, 2_000_000, false, 90},
		// Sub-second stamps: 1 ms apart still orders.
		{"millisecond resolution", 2_000_001, 2_000_000, false, 90},
		// Same change time: the later write breaks the tie.
		{"tie broken by mtime", 2_000_000, 2_000_000, true, 90},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usageModels.reset()
			h := newUsageRelayHarness(t)
			dir := h.sessionsDir()
			pA := writeMarkerFile(t, dir, "a", fmt.Sprintf(`,"rate_week_pct":90,"rate_week_reset_at":2000,"statusline_changed_ms":%d`, tc.changedA))
			pB := writeMarkerFile(t, dir, "b", fmt.Sprintf(`,"rate_week_pct":10,"rate_week_reset_at":1000,"statusline_changed_ms":%d`, tc.changedB))
			older, newer := time.Now().Add(-time.Minute), time.Now()
			mA, mB := older, newer
			if tc.mtimeANewer {
				mA, mB = newer, older
			}
			if err := os.Chtimes(pA, mA, mA); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(pB, mB, mB); err != nil {
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
			_ = json.Unmarshal([]byte(h.usageBodies[0]), &req)
			if req.SevenDay == nil || req.SevenDay.UsedPercent != tc.want {
				t.Errorf("seven_day = %+v, want %v", req.SevenDay, tc.want)
			}
		})
	}
}
