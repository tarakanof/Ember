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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

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
	cfgDir := filepath.Join(home, ".config", "ember")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envContent := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return &hookHarness{t: t, home: home, srv: srv, posts: posts, deletes: deletes, bodies: bodies}
}

func (h *hookHarness) sessionsDir() string {
	return filepath.Join(h.home, ".local", "state", "ember", "sessions")
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

// A normal turn end must leave `running`: agent_completed fires only for
// background sessions under agent view (#257).
func TestHook_Stop_UpsertsDoneAndKeepsMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"hook_event_name":"Stop","session_id":"abc","cwd":"/repo","stop_hook_active":false,` +
		`"last_assistant_message":"\n\nRefactor done.\nDetails follow.","background_tasks":[],"session_crons":[]}`
	dispatchHookForTest(t, "stop", []byte(body))
	if h.deletes.Load() != 0 {
		t.Errorf("stop should not delete; deletes = %d, want 0", h.deletes.Load())
	}
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"state":"done"`) || !strings.Contains(got, `"message":"Refactor done."`) {
		t.Errorf("stop POST = %s, want state done with the reply's first line", got)
	}
	m, err := os.ReadFile(markerP)
	if err != nil {
		t.Fatalf("marker should be kept for the heartbeat until SessionEnd: %v", err)
	}
	if !strings.Contains(string(m), `"state":"done"`) {
		t.Errorf("marker = %s, want state done so the heartbeat re-posts done", m)
	}
}

func TestHook_Stop_BackgroundTasks(t *testing.T) {
	cases := []struct {
		name, tasks, want string
	}{
		{"subagent keeps running", `[{"id":"t1","type":"subagent","status":"running","agent_type":"Explore"}]`, "running"},
		{"workflow keeps running", `[{"id":"t1","type":"workflow","status":"running"}]`, "running"},
		{"shell is done", `[{"id":"t1","type":"shell","status":"running","command":"npm run dev"}]`, "done"},
		{"monitor is done", `[{"id":"t1","type":"monitor","status":"running"}]`, "done"},
		{"mixed keeps running", `[{"type":"shell"},{"type":"teammate"}]`, "running"},
		{"odd shape is done", `{"nope":1}`, "done"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHookHarness(t)
			dir := h.sessionsDir()
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			markerP := filepath.Join(dir, "abc.json")
			if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			body := `{"hook_event_name":"Stop","session_id":"abc","cwd":"/repo","last_assistant_message":"ok","background_tasks":` + c.tasks + `}`
			dispatchHookForTest(t, "stop", []byte(body))
			if m, _ := os.ReadFile(markerP); !strings.Contains(string(m), `"state":"`+c.want+`"`) {
				t.Errorf("marker = %s, want state %s", m, c.want)
			}
			if wantPosts := map[string]int32{"running": 0, "done": 1}[c.want]; h.posts.Load() != wantPosts {
				t.Errorf("posts = %d, want %d", h.posts.Load(), wantPosts)
			}
		})
	}
}

func TestHook_Notification_TypeMapping(t *testing.T) {
	cases := []struct {
		typ, prev, want string // want "" = no POST
	}{
		{"idle_prompt", "running", "done"},
		{"elicitation_dialog", "running", "waiting"},
		{"elicitation_url_dialog", "running", "waiting"},
		{"elicitation_complete", "waiting", "running"},
		{"elicitation_response", "waiting", "running"},
		{"elicitation_response", "done", ""}, // only ends a wait
		{"idle_prompt", "done", ""},          // keeps Stop's reply line
		{"idle_prompt", "error", ""},         // never hides an error
		{"permission_prompt", "running", "waiting"},
		{"agent_needs_input", "running", "waiting"},
		{"agent_completed", "running", "done"},
		{"quota_auto_resume_fired", "error", "running"},
		{"quota_auto_resume_stale", "error", "waiting"},
		{"quota_auto_resume_disabled", "error", "error"},
		{"auth_success", "running", ""},
	}
	for _, c := range cases {
		t.Run(c.typ+"/"+c.prev, func(t *testing.T) {
			h := newHookHarness(t)
			if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
				t.Fatal(err)
			}
			prev := `{"source":"test-mbp","tool":"claude","session":"abc","state":"` + c.prev + `"}`
			if err := os.WriteFile(filepath.Join(h.sessionsDir(), "abc.json"), []byte(prev), 0o600); err != nil {
				t.Fatal(err)
			}
			body := `{"hook_event_name":"Notification","session_id":"abc","cwd":"/repo","message":"m","title":"t","notification_type":"` + c.typ + `"}`
			dispatchHookForTest(t, "notification", []byte(body))
			if c.want == "" {
				if h.posts.Load() != 0 {
					t.Errorf("posts = %d, want 0", h.posts.Load())
				}
				return
			}
			if h.posts.Load() != 1 {
				t.Fatalf("posts = %d, want 1", h.posts.Load())
			}
			if !strings.Contains((*h.bodies)[0], `"state":"`+c.want+`"`) {
				t.Errorf("POST = %s, want state %s", (*h.bodies)[0], c.want)
			}
		})
	}
}

// Claude Code's StopFailure input is error / error_details /
// last_assistant_message; error_type and error_message don't exist.
func TestHook_StopFailure_Message(t *testing.T) {
	cases := []struct{ body, want string }{
		{`"error":"rate_limit","error_details":"429 Too Many Requests","last_assistant_message":"API Error: Rate limit reached"`, "rate limited: 429 Too Many Requests"},
		{`"error":"overloaded"`, "API overloaded"},
		{`"error":"unknown","last_assistant_message":"API Error: boom"`, "API Error: boom"},
		{`"error":"some_new_kind"`, "some new kind"},
		{``, "error"},
	}
	for _, c := range cases {
		h := newHookHarness(t)
		body := `{"hook_event_name":"StopFailure","session_id":"abc","cwd":"/repo"`
		if c.body != "" {
			body += "," + c.body
		}
		dispatchHookForTest(t, "stop-failure", []byte(body+"}"))
		if h.posts.Load() != 1 {
			t.Fatalf("%s: posts = %d, want 1", c.body, h.posts.Load())
		}
		got := (*h.bodies)[0]
		if !strings.Contains(got, `"state":"error"`) || !strings.Contains(got, `"message":"`+c.want+`"`) {
			t.Errorf("%s: POST = %s, want message %q", c.body, got, c.want)
		}
	}
}

// holdLock takes the session lock from another open file description (as a
// separate hook process would) until the returned func is called.
func holdLock(t *testing.T, lockP string) func() {
	t.Helper()
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = withLockEx(lockP, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	var once sync.Once
	stop := func() { once.Do(func() { close(release) }) }
	t.Cleanup(stop)
	return stop
}

// A hook must not queue behind a wedged lock holder for seconds (#258): it
// gives up after hookLockWait and drops the update.
func TestHook_LockWaitIsBounded(t *testing.T) {
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	holdLock(t, filepath.Join(h.sessionsDir(), "abc.lock"))
	cfg, _ := loadConfig()
	start := time.Now()
	dispatchHook(context.Background(), "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/repo","prompt":"hi"}`), cfg)
	if took := time.Since(start); took > hookLockWait(cfg)+300*time.Millisecond {
		t.Errorf("hook took %v behind a held lock, want <= %v", took, hookLockWait(cfg))
	}
	if h.posts.Load() != 0 {
		t.Errorf("posts = %d, want 0 (update dropped on contention)", h.posts.Load())
	}
}

// SessionEnd shares a 1.5 s budget: it never waits out the lock and still
// sends its DELETE.
// An MCP elicitation answer must not end a permission wait.
func TestHook_ElicitationResponse_KeepsPermissionWait(t *testing.T) {
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	prev := `{"source":"test-mbp","tool":"claude","session":"abc","state":"waiting","pending_permission":"fp"}`
	if err := os.WriteFile(filepath.Join(h.sessionsDir(), "abc.json"), []byte(prev), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/repo","notification_type":"elicitation_response"}`))
	if h.posts.Load() != 0 {
		t.Errorf("posts = %d, want 0", h.posts.Load())
	}
}

func TestHook_SessionStart_ClearsMarkerWhenLockBusy(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	holdLock(t, filepath.Join(dir, "abc.lock"))
	dispatchHookForTest(t, "session-start", []byte(`{"session_id":"abc","cwd":"/repo","source":"clear"}`))
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("marker should be cleared even when the lock is busy")
	}
}

// The upsert stamps state_changed_at on a state change and keeps it while
// the state holds, so done/error expire from when they began.
func TestHandleUpsert_StateChangedAt(t *testing.T) {
	h := newHookHarness(t)
	_ = h
	t0 := time.Unix(1_800_000_000, 0)
	now := t0
	old := hookNow
	hookNow = func() time.Time { return now }
	t.Cleanup(func() { hookNow = old })
	read := func() marker {
		var m marker
		raw, _ := os.ReadFile(filepath.Join(h.sessionsDir(), "abc.json"))
		_ = json.Unmarshal(raw, &m)
		return m
	}
	dispatchHookForTest(t, "stop", []byte(`{"session_id":"abc","cwd":"/repo","last_assistant_message":"a"}`))
	if got := read().StateChangedAt; got != t0.Unix() {
		t.Fatalf("state_changed_at = %d, want %d", got, t0.Unix())
	}
	now = t0.Add(20 * time.Second)
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/repo","notification_type":"agent_completed"}`))
	if got := read().StateChangedAt; got != t0.Unix() {
		t.Errorf("same state restamped: %d, want %d", got, t0.Unix())
	}
	now = t0.Add(40 * time.Second)
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/repo","prompt":"x"}`))
	if got := read().StateChangedAt; got != now.Unix() {
		t.Errorf("state change not stamped: %d, want %d", got, now.Unix())
	}
}

func TestHook_SessionEnd_DoesNotBlockOnLock(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	holdLock(t, filepath.Join(dir, "abc.lock"))
	start := time.Now()
	dispatchHookForTest(t, "session-end", []byte(`{"session_id":"abc","cwd":"/repo","reason":"prompt_input_exit"}`))
	if took := time.Since(start); took > sessionEndLockWait+300*time.Millisecond {
		t.Errorf("session-end took %v behind a held lock", took)
	}
	if h.deletes.Load() != 1 {
		t.Errorf("deletes = %d, want 1", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("marker should be removed even when the lock is busy: %v", err)
	}
}

// The DELETE gets at most sessionEndHTTPBudget even with a long HookTimeoutMs.
func TestHook_SessionEnd_CapsDeleteTimeout(t *testing.T) {
	h := newHookHarness(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_HOOK_TIMEOUT_MS=5000\n"
	if err := os.WriteFile(filepath.Join(h.home, ".config", "ember", "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	dispatchHookForTest(t, "session-end", []byte(`{"session_id":"abc","cwd":"/repo","reason":"other"}`))
	if took := time.Since(start); took > sessionEndHTTPBudget+400*time.Millisecond {
		t.Errorf("session-end took %v against a hung server, want <= ~%v", took, sessionEndHTTPBudget)
	}
}

// Bodies are raw JSON in Claude Code's wire shape (SessionEnd sends "reason"),
// not a marshalled hookInput, so a wrong struct tag can't round-trip and pass.
func TestHook_SessionEnd_DeletesMarker(t *testing.T) {
	for _, reason := range []string{"prompt_input_exit", "clear", "resume", "logout", "other"} {
		t.Run(reason, func(t *testing.T) {
			h := newHookHarness(t)
			dir := h.sessionsDir()
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			markerP := filepath.Join(dir, "abc.json")
			if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			body := `{"hook_event_name":"SessionEnd","session_id":"abc","cwd":"/repo","reason":"` + reason + `"}`
			dispatchHookForTest(t, "session-end", []byte(body))
			if h.deletes.Load() != 1 {
				t.Errorf("session-end reason=%s should delete; deletes = %d, want 1", reason, h.deletes.Load())
			}
			if _, err := os.Stat(markerP); !os.IsNotExist(err) {
				t.Errorf("marker should be removed after session-end reason=%s", reason)
			}
		})
	}
}

func TestHook_SessionEnd_UnknownReasonKeepsMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "session-end", []byte(`{"hook_event_name":"SessionEnd","session_id":"abc","cwd":"/repo","reason":"bypass_permissions_disabled"}`))
	if h.deletes.Load() != 0 {
		t.Errorf("unknown reason must not delete; deletes = %d", h.deletes.Load())
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
		NotificationType: "auth_success", Message: "logged in"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "notification", body)
	if h.posts.Load() != 0 {
		t.Errorf("auth_success should not POST; posts = %d", h.posts.Load())
	}
}

func TestHook_Notification_AgentNeedsInput_UpsertsWaiting(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "Notification", SessionID: "abc", CWD: "/repo",
		NotificationType: "agent_needs_input", Message: "needs your input"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "notification", body)
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	if !strings.Contains((*h.bodies)[0], `"state":"waiting"`) {
		t.Errorf("body missing state=waiting: %q", (*h.bodies)[0])
	}
	if !strings.Contains((*h.bodies)[0], `"message":"needs your input"`) {
		t.Errorf("body missing message: %q", (*h.bodies)[0])
	}
}

func TestHook_Notification_AgentCompleted_UpsertsDone(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "Notification", SessionID: "abc", CWD: "/repo",
		NotificationType: "agent_completed", Message: "all done"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "notification", body)
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	if !strings.Contains((*h.bodies)[0], `"state":"done"`) {
		t.Errorf("body missing state=done: %q", (*h.bodies)[0])
	}
	if !strings.Contains((*h.bodies)[0], `"message":"all done"`) {
		t.Errorf("body missing message: %q", (*h.bodies)[0])
	}
	if _, err := os.Stat(filepath.Join(h.sessionsDir(), "abc.json")); err != nil {
		t.Errorf("marker file missing after agent_completed (should upsert, not delete): %v", err)
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

func dispatchHookForTest(t *testing.T, event string, stdin []byte) {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	dispatchHook(context.Background(), event, stdin, cfg)
}

func TestDispatchHook_UpsertEnrichesWithSourceColor(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_SOURCE_COLOR=#aa66ff\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}

	in := hookInput{HookEventName: "UserPromptSubmit", SessionID: "test-session-xyz", CWD: "/repo", Prompt: "hi"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "user-prompt-submit", body)

	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"source_color":"#aa66ff"`) {
		t.Errorf("body missing source_color: %s", got)
	}
}

func TestDispatchHook_PreservesContextPctWhenEnabled(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_CONTEXT_PCT_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := sanitizeSessionID("s1", "/r")
	if err := os.WriteFile(filepath.Join(dir, sid+".json"),
		[]byte(`{"source":"mbp","tool":"claude","session":"`+sid+`","state":"running","context_pct":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"go test"}}`))
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"context_pct":42`) {
		t.Errorf("hook should preserve statusline context_pct=42: %s", got)
	}
}

func TestDispatchHook_PreservesWeekFieldsOnMarkerButStripsFromStatusPost(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := sanitizeSessionID("s1", "/r")
	markerP := filepath.Join(dir, sid+".json")
	seed := `{"source":"test-mbp","tool":"claude","session":"` + sid + `","state":"running",` +
		`"rate_week_pct":35,"rate_week_reset_at":1778700000,"rate_week_reset_label":"MON"}`
	if err := os.WriteFile(markerP, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"go test"}}`))

	posted := (*h.bodies)[0]
	if strings.Contains(posted, `"rate_week_pct"`) {
		t.Errorf("rate_week_pct must not appear on the /v1/status POST body: %s", posted)
	}

	raw, err := os.ReadFile(markerP)
	if err != nil {
		t.Fatal(err)
	}
	var req StatusRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.RateWeekPct == nil || *req.RateWeekPct != 35 {
		t.Errorf("hook clobbered rate_week_pct on marker: got %v", req.RateWeekPct)
	}
	if req.RateWeekResetAt != 1778700000 {
		t.Errorf("hook clobbered rate_week_reset_at on marker: got %d", req.RateWeekResetAt)
	}
	if req.RateWeekResetLabel != "MON" {
		t.Errorf("hook clobbered rate_week_reset_label on marker: got %q", req.RateWeekResetLabel)
	}
}

func TestDispatchHook_ClearsContextPctWhenDisabled(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_CONTEXT_PCT_ENABLED=false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := sanitizeSessionID("s1", "/r")
	if err := os.WriteFile(filepath.Join(dir, sid+".json"),
		[]byte(`{"source":"mbp","tool":"claude","session":"`+sid+`","state":"running","context_pct":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"go test"}}`))
	got := (*h.bodies)[0]
	if strings.Contains(got, `"context_pct"`) {
		t.Errorf("disabled: context_pct should be cleared, got: %s", got)
	}
}

func TestHandleUpsert_PreservesRateWindowPct(t *testing.T) {
	h := newHookHarness(t)

	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "s1.json")
	initial := `{"source":"test-mbp","tool":"claude","session":"s1","state":"running","rate_window_pct":42}`
	if err := os.WriteFile(markerP, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	in := hookInput{HookEventName: "UserPromptSubmit", SessionID: "s1", CWD: "/repo", Prompt: "do something"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "user-prompt-submit", body)

	raw, err := os.ReadFile(markerP)
	if err != nil {
		t.Fatalf("marker missing after upsert: %v", err)
	}
	var got StatusRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("marker unmarshal failed: %v", err)
	}
	if got.RateWindowPct == nil {
		t.Errorf("marker: rate_window_pct is nil, want 42")
	} else if *got.RateWindowPct != 42 {
		t.Errorf("marker: rate_window_pct = %d, want 42", *got.RateWindowPct)
	}

	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	postBody := (*h.bodies)[0]
	if !strings.Contains(postBody, `"rate_window_pct":42`) {
		t.Errorf("POST body missing rate_window_pct=42: %s", postBody)
	}

	markerP2 := filepath.Join(dir, "s2.json")
	noRate := `{"source":"test-mbp","tool":"claude","session":"s2","state":"running"}`
	if err := os.WriteFile(markerP2, []byte(noRate), 0o600); err != nil {
		t.Fatal(err)
	}
	in2 := hookInput{HookEventName: "UserPromptSubmit", SessionID: "s2", CWD: "/repo", Prompt: "again"}
	body2, _ := json.Marshal(in2)
	dispatchHookForTest(t, "user-prompt-submit", body2)

	raw2, err := os.ReadFile(markerP2)
	if err != nil {
		t.Fatalf("marker2 missing: %v", err)
	}
	var got2 StatusRequest
	if err := json.Unmarshal(raw2, &got2); err != nil {
		t.Fatalf("marker2 unmarshal failed: %v", err)
	}
	if got2.RateWindowPct != nil {
		t.Errorf("marker2: rate_window_pct should be nil when not set, got %d", *got2.RateWindowPct)
	}
}

func TestDispatchHook_PreToolUseSetsActivity(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_ACTIVITY_DETAIL_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"npm test"}}`)
	dispatchHookForTest(t, "pre-tool-use", body)
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"activity":"Bash: npm test"`) {
		t.Errorf("pre-tool-use body missing activity: %s", got)
	}
	if !strings.Contains(got, `"state":"running"`) {
		t.Errorf("pre-tool-use should be running: %s", got)
	}
}

func TestDispatchHook_ActivityDisabledOmitsField(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_ACTIVITY_DETAIL_ENABLED=false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"npm test"}}`)
	dispatchHookForTest(t, "pre-tool-use", body)
	got := (*h.bodies)[0]
	if strings.Contains(got, `"activity"`) {
		t.Errorf("activity should be omitted when disabled: %s", got)
	}
}

func TestHandleUpsert_StampsRateBottomBar(t *testing.T) {
	dir := t.TempDir()
	markerP := markerPath(dir, "sess")
	lockP := lockPath(dir, "sess")
	cfg := Config{Common: producer.Common{Source: "mbp", ServerURL: "http://127.0.0.1:1"}, Gauges: producer.Gauges{RateBottomBarEnabled: true}, HookTimeoutMs: 500}
	client := NewClient(cfg)
	handleUpsert(context.Background(), cfg, client, "sess", "running", "msg", "", markerP, lockP)

	raw, err := readMarker(markerP)
	if err != nil {
		t.Fatal(err)
	}
	var req StatusRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if !req.RateBottomBar {
		t.Error("marker missing RateBottomBar=true")
	}
}

func TestDispatchHook_PermissionRequestSetsActivity(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Edit","tool_input":{"file_path":"/repo/render.go"}}`)
	dispatchHookForTest(t, "permission-request", body)
	got := (*h.bodies)[0]
	if !strings.Contains(got, `"state":"waiting"`) {
		t.Errorf("permission-request should be waiting: %s", got)
	}
	if !strings.Contains(got, `"activity":"Edit: render.go"`) {
		t.Errorf("permission-request body missing activity: %s", got)
	}
}

func TestDispatchHook_TrailAccumulatesNewestFirst(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_ACTIVITY_TRAIL_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"a"}}`))
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Edit","tool_input":{"file_path":"/r/b.go"}}`))
	got := (*h.bodies)[1]
	if !strings.Contains(got, `"activity":"Edit: b.go · Bash: a"`) {
		t.Errorf("second body trail wrong: %s", got)
	}
}

func TestDispatchHook_TrailResetsOnNewPrompt(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"a"}}`))
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"s1","cwd":"/r","prompt":"hi"}`))
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Edit","tool_input":{"file_path":"/r/b.go"}}`))
	got := (*h.bodies)[2]
	if !strings.Contains(got, `"activity":"Edit: b.go"`) || strings.Contains(got, "Bash: a") {
		t.Errorf("trail should reset after a new prompt: %s", got)
	}
}

func TestDispatchHook_TrailDisabledKeepsSingleAction(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_ACTIVITY_TRAIL_ENABLED=false\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"a"}}`))
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Edit","tool_input":{"file_path":"/r/b.go"}}`))
	got := (*h.bodies)[1]
	if !strings.Contains(got, `"activity":"Edit: b.go"`) || strings.Contains(got, "Bash: a") {
		t.Errorf("trail disabled should show single action only: %s", got)
	}
}

func TestDispatchHook_SetsContextNumberFlag(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_CONTEXT_NUMBER_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/r","tool_name":"Bash","tool_input":{"command":"x"}}`))
	if got := (*h.bodies)[0]; !strings.Contains(got, `"context_number":true`) {
		t.Errorf("body missing context_number=true: %s", got)
	}
}

func TestDispatchHook_DeletePathUnchanged(t *testing.T) {
	h := newHookHarness(t)
	cfgDir := filepath.Join(h.home, ".config", "ember")
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\nEMBER_SOURCE_COLOR=#aa66ff\nEMBER_CONTEXT_PCT_ENABLED=true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc.json"), []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "SessionEnd", SessionID: "abc", CWD: "/repo", EndReason: "prompt_input_exit"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "session-end", body)

	if h.deletes.Load() != 1 {
		t.Fatalf("deletes = %d, want 1", h.deletes.Load())
	}
	got := (*h.bodies)[0]
	if strings.Contains(got, `"context_pct"`) || strings.Contains(got, `"source_color"`) {
		t.Errorf("DELETE body must not carry G.3 fields: %s", got)
	}
}

// Hooks write the marker under the lock and POST after releasing it, so a
// slow server can't make a parallel hook (say Stop) drop its marker write
// and leave the heartbeat re-posting a stale running.
func TestHook_PostsOutsideLock(t *testing.T) {
	h := newHookHarness(t)
	lockP := filepath.Join(h.sessionsDir(), "abc.lock")
	var lockFree atomic.Bool
	var calls atomic.Int32
	h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			lockFree.Store(withLockExWait(lockP, 100*time.Millisecond, func() error { return nil }) == nil)
		}
		w.WriteHeader(204)
	})
	dispatchHookForTest(t, "stop", []byte(`{"session_id":"abc","cwd":"/repo","last_assistant_message":"ok"}`))
	if !lockFree.Load() {
		t.Error("session lock was held while the hook POST was in flight")
	}
}
