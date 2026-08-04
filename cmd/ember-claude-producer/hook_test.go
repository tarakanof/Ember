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

// Stop no longer deletes: the session stays present (sustained by the heartbeat
// tick) until the Claude Code window closes (SessionEnd). Deleting on every Stop
// dropped the display to the idle robot between turns and during text
// generation, when no hook fires.
func TestHook_Stop_KeepsMarkerForHeartbeat(t *testing.T) {
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
	if h.deletes.Load() != 0 {
		t.Errorf("stop should not delete; deletes = %d, want 0", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); err != nil {
		t.Errorf("marker should be preserved after stop (heartbeat keeps it present until SessionEnd): %v", err)
	}
}

// SessionEnd is now the path that clears the session when the window closes.
func TestHook_SessionEnd_DeletesMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "SessionEnd", SessionID: "abc", CWD: "/repo", EndReason: "prompt_input_exit"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "session-end", body)
	if h.deletes.Load() != 1 {
		t.Errorf("session-end should delete; deletes = %d, want 1", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("marker should be removed after session-end")
	}
}

// TestHook_SessionEnd_Clear_DeletesMarker is the Task-9 /clear-ghost
// regression test: Claude Code fires SessionEnd(reason="clear") and issues a
// new session id, but before this fix "clear" fell through the switch,
// leaving the old marker (and its still-alive owner PID) to display as a
// phantom "running" session.
func TestHook_SessionEnd_Clear_DeletesMarker(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "abc.json")
	if err := os.WriteFile(markerP, []byte(`{"source":"test-mbp","tool":"claude","session":"abc","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "SessionEnd", SessionID: "abc", CWD: "/repo", EndReason: "clear"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "session-end", body)
	if h.deletes.Load() != 1 {
		t.Errorf("session-end reason=clear should delete; deletes = %d, want 1", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("marker should be removed after session-end reason=clear")
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

// TestHook_Notification_AgentNeedsInput_UpsertsWaiting is the #75 regression:
// Claude Code's Notification hook can fire with notification_type
// "agent_needs_input" — an explicit "waiting for the user" signal that should
// upsert the same "waiting" state as the existing permission_prompt path.
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

// TestHook_Notification_AgentCompleted_UpsertsDone is the #75 regression:
// notification_type "agent_completed" is an explicit "finished" signal and
// should upsert state "done" (not delete the marker — the process-ancestry
// walk / SessionEnd remain the source of truth for clearing a session).
func TestHook_Notification_AgentCompleted_UpsertsDone(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "Notification", SessionID: "abc", CWD: "/repo",
		NotificationType: "agent_completed", NotificationMessage: "all done"}
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

// dispatchHookForTest is the testable seam: calls dispatchHook with a context+timeout.
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
	// Stage producer.env with source_color
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

// TestDispatchHook_PreservesWeekFieldsOnMarkerButStripsFromStatusPost verifies
// two things at once: a hook event must not clobber the statusline-owned
// weekly (rate_week_*) fields on the marker file (so the next heartbeat tick
// can still relay them to /v1/usage), AND those fields must never appear on
// the POST /v1/status body itself — they're marker-only.
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

	// Pre-write a marker that already carries rate_window_pct=42 (set by statusline).
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "s1.json")
	initial := `{"source":"test-mbp","tool":"claude","session":"s1","state":"running","rate_window_pct":42}`
	if err := os.WriteFile(markerP, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	// Fire a hook event that goes through handleUpsert.
	in := hookInput{HookEventName: "UserPromptSubmit", SessionID: "s1", CWD: "/repo", Prompt: "do something"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "user-prompt-submit", body)

	// Assert: marker still carries rate_window_pct=42.
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

	// Assert: POSTed body also carries rate_window_pct=42.
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", h.posts.Load())
	}
	postBody := (*h.bodies)[0]
	if !strings.Contains(postBody, `"rate_window_pct":42`) {
		t.Errorf("POST body missing rate_window_pct=42: %s", postBody)
	}

	// Sub-case: marker with NO rate — upsert must not introduce a spurious value.
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
	cfg := Config{Source: "mbp", ServerURL: "http://x", RateBottomBarEnabled: true}
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
	// Create a marker so handleDelete has something to remove
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
	// DeleteRequest must NOT carry context_pct or source_color
	if strings.Contains(got, `"context_pct"`) || strings.Contains(got, `"source_color"`) {
		t.Errorf("DELETE body must not carry G.3 fields: %s", got)
	}
}
