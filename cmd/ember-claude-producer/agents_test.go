package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type agentsFixture struct {
	h             *hookHarness
	w             *agentsWatcher
	cfg           Config
	clock         time.Time
	out           string
	err           error
	calls         int
	sessionsMtime time.Time
}

func newAgentsFixture(t *testing.T) *agentsFixture {
	t.Helper()
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	claudeSessions := filepath.Join(h.home, ".claude", "sessions")
	if err := os.MkdirAll(claudeSessions, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	f := &agentsFixture{h: h, cfg: cfg, clock: time.Unix(1_800_000_000, 0)}
	f.w = newAgentsWatcher(h.sessionsDir(), claudeSessions)
	f.w.now = func() time.Time { return f.clock }
	f.w.run = func(context.Context) ([]byte, error) {
		f.calls++
		return []byte(f.out), f.err
	}
	return f
}

func (f *agentsFixture) writeMarker(t *testing.T, id string, m marker) []byte {
	t.Helper()
	m.Source, m.Tool, m.Session = "test-mbp", "claude", id
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(f.h.sessionsDir(), id+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *agentsFixture) readMarker(t *testing.T, id string) marker {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.h.sessionsDir(), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *agentsFixture) step(d time.Duration) bool {
	f.clock = f.clock.Add(d)
	return f.w.step(context.Background(), f.cfg, NewDaemonClient(f.cfg))
}

func (f *agentsFixture) activate(t *testing.T) {
	t.Helper()
	f.step(time.Second)
	if f.calls != 0 {
		t.Fatalf("calls = %d; becoming active must not call the CLI by itself", f.calls)
	}
	f.touchSessions(t, f.clock.String())
}

func (f *agentsFixture) touchSessions(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(f.h.home, ".claude", "sessions", "4242.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitingMarker() marker {
	var m marker
	m.State, m.Message = "waiting", "approve Bash"
	m.PendingPermission = "fp"
	return m
}

func TestParseAgents(t *testing.T) {
	out := `[
	  {"pid":1,"cwd":"/a","kind":"interactive","startedAt":1,"sessionId":"s1","status":"waiting","waitingFor":"permission prompt"},
	  {"id":"ab","cwd":"/b","kind":"background","startedAt":1,"sessionId":"s2","state":"done"},
	  {"pid":3,"cwd":"/c","kind":"interactive","startedAt":1,"status":"busy"}
	]`
	rows, err := parseAgents([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows["s1"].WaitingFor != "permission prompt" || rows["s1"].PID != 1 {
		t.Fatalf("rows = %+v; want only s1 (s2 has no live process, the third no sessionId)", rows)
	}
	if _, err := parseAgents([]byte("Usage: claude agents")); err == nil {
		t.Fatal("non-JSON output (an older CLI) must be an error")
	}
}

func TestWantFor(t *testing.T) {
	perm := func(state, msg string) marker {
		var m marker
		m.State, m.Message, m.PendingPermission = state, msg, "fp"
		return m
	}
	plain := func(state, msg string) marker {
		var m marker
		m.State, m.Message = state, msg
		return m
	}
	agentsWait := plain("waiting", "permission prompt")
	agentsWait.AgentsWait = true
	bgWake := plain("running", "Agent")
	bgWake.BackgroundWake = true
	cases := []struct {
		name       string
		m          marker
		status     string
		waitingFor string
		want       correction
		ok         bool
	}{
		{"approved", perm("waiting", "approve Bash"), "busy", "", correction{"running", "Bash"}, true},
		{"approved after Notification message", perm("waiting", "Claude needs your permission"), "busy", "", correction{"running", "resumed"}, true},
		{"dialog dismissed", perm("waiting", "approve Bash"), "idle", "", correction{"done", interruptedMessage}, true},
		{"missed wait", plain("running", "Bash"), "waiting", "input needed", correction{"waiting", "input needed"}, true},
		{"interrupted", plain("running", "essay"), "idle", "", correction{"done", interruptedMessage}, true},
		{"own wait ends", agentsWait, "busy", "", correction{"running", "resumed"}, true},
		{"agree running", plain("running", "Bash"), "busy", "", correction{}, false},
		{"agree waiting", perm("waiting", "approve Bash"), "waiting", "permission prompt", correction{}, false},
		{"quota pause is the hooks'", plain("waiting", "usage limit"), "idle", "", correction{}, false},
		{"agent_needs_input is the hooks'", plain("waiting", "needs input"), "busy", "", correction{}, false},
		{"background wake hold", bgWake, "idle", "", correction{}, false},
		{"background wake still sees waits", bgWake, "waiting", "permission prompt", correction{"waiting", "permission prompt"}, true},
		{"unknown status", plain("running", "Bash"), "sleeping", "", correction{}, false},
		{"unknown status on wait", perm("waiting", "approve Bash"), "", "", correction{}, false},
	}
	for _, c := range cases {
		got, ok := wantFor(c.m, agentRow{SessionID: "s", Status: c.status, WaitingFor: c.waitingFor})
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v,%v want %+v,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestAgentsPollEnabled(t *testing.T) {
	h := newHookHarness(t)
	if !agentsPollEnabled() {
		t.Fatal("default must be on")
	}
	env := filepath.Join(h.home, ".config", "ember", "producer.env")
	b, _ := os.ReadFile(env)
	if err := os.WriteFile(env, append(b, []byte("EMBER_CLAUDE_AGENTS_POLL=off\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if agentsPollEnabled() {
		t.Fatal("producer.env off must disable")
	}
	t.Setenv("EMBER_CLAUDE_AGENTS_POLL", "1")
	if !agentsPollEnabled() {
		t.Fatal("env var must override producer.env")
	}
}

func TestAgents_NoActiveMarker_NoCall(t *testing.T) {
	f := newAgentsFixture(t)
	var done marker
	done.State = "done"
	f.writeMarker(t, "s1", done)
	f.touchSessions(t, "a")
	for i := 0; i < 40; i++ {
		f.step(time.Second)
	}
	if f.calls != 0 {
		t.Fatalf("calls = %d; the CLI must not run without a running/waiting marker", f.calls)
	}
}

func TestAgents_CallsOnSessionsChangeOrFallback(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State = "running"
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":1,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	f.step(time.Second)
	f.step(time.Second)
	if f.calls != 0 {
		t.Fatalf("calls = %d while ~/.claude/sessions is unchanged, want 0", f.calls)
	}
	f.touchSessions(t, "busy->waiting")
	f.step(time.Second)
	if f.calls != 1 {
		t.Fatalf("calls = %d after a sessions change, want 1", f.calls)
	}
	for i := 1; i < int(agentsFallbackEvery/time.Second); i++ {
		f.step(time.Second)
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d before the fallback, want 1", f.calls)
	}
	f.step(time.Second)
	if f.calls != 2 {
		t.Fatalf("calls = %d at the fallback, want 2", f.calls)
	}
}

func TestAgents_ApprovedWaitTurnsRunningAfterConfirmation(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", waitingMarker())
	f.out = `[{"pid":1,"sessionId":"s1","status":"busy"}]`
	f.activate(t)
	f.step(time.Second)
	if got := f.readMarker(t, "s1"); got.State != "waiting" || f.h.posts.Load() != 0 {
		t.Fatalf("one snapshot must not change anything; state=%s posts=%d", got.State, f.h.posts.Load())
	}
	f.step(time.Second)
	f.step(time.Second)
	got := f.readMarker(t, "s1")
	if got.State != "running" || got.Message != "Bash" || got.PendingPermission != "" {
		t.Fatalf("marker = %+v; want running/Bash with the pending wait cleared", got)
	}
	if got.ResumedTool != "Bash" || got.ResumedAt == 0 {
		t.Fatalf("resume not recorded; a late permission_prompt would re-enter waiting: %+v", got.ToolTrack)
	}
	if f.h.posts.Load() != 1 || !strings.Contains((*f.h.bodies)[0], `"state":"running"`) {
		t.Fatalf("posts=%d bodies=%v; want one running POST", f.h.posts.Load(), *f.h.bodies)
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 (snapshot + confirmation)", f.calls)
	}
}

func TestAgents_InterruptedTurnBecomesDone(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.Message = "running", "write an essay"
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":1,"sessionId":"s1","status":"idle"}]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "done" || got.Message != interruptedMessage {
		t.Fatalf("marker = %s/%q; want done/%q", got.State, got.Message, interruptedMessage)
	}
}

func TestAgents_MarkerChangedBetweenSnapshots_NoCorrection(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.Message = "running", "Bash"
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":1,"sessionId":"s1","status":"idle"}]`
	f.activate(t)
	f.step(time.Second)
	m.Message = "Edit"
	f.writeMarker(t, "s1", m)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "running" || got.Message != "Edit" {
		t.Fatalf("marker = %s/%q; a fresh hook write must not be overridden", got.State, got.Message)
	}
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "done" {
		t.Fatalf("a disagreement that persists on the new bytes must still apply; state=%s", got.State)
	}
}

func TestAgents_DisagreementGoneOnRecheck_NoCorrection(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", waitingMarker())
	f.out = `[{"pid":1,"sessionId":"s1","status":"busy"}]`
	f.activate(t)
	f.step(time.Second)
	f.out = `[{"pid":1,"sessionId":"s1","status":"waiting","waitingFor":"permission prompt"}]`
	f.step(2 * time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "waiting" || f.h.posts.Load() != 0 {
		t.Fatalf("state=%s posts=%d; a one-off disagreement must not apply", got.State, f.h.posts.Load())
	}
}

func TestAgents_UnlistedSession_ReapedOnlyWhenOwnerDead(t *testing.T) {
	f := newAgentsFixture(t)
	alive := true
	orig := ownerAlive
	ownerAlive = func(int, string) bool { return alive }
	t.Cleanup(func() { ownerAlive = orig })
	m := waitingMarker()
	m.OwnerPID = 4242
	f.writeMarker(t, "s1", m)
	f.out = `[]`
	f.activate(t)
	f.step(time.Second)
	if _, err := os.Stat(filepath.Join(f.h.sessionsDir(), "s1.json")); err != nil {
		t.Fatal("an unlisted session with a live owner (headless, SDK, nested) must stay")
	}
	alive = false
	f.touchSessions(t, "exit")
	f.step(time.Second)
	if _, err := os.Stat(filepath.Join(f.h.sessionsDir(), "s1.json")); !os.IsNotExist(err) {
		t.Fatal("marker of a dead owner should be reaped")
	}
	if f.h.deletes.Load() != 1 {
		t.Fatalf("deletes = %d, want 1", f.h.deletes.Load())
	}
}

func TestAgents_CommandFailure_BacksOff(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State = "running"
	f.writeMarker(t, "s1", m)
	f.err = errors.New("exit status 1")
	f.activate(t)
	f.step(time.Second)
	for i := 0; i < 60; i++ {
		f.touchSessions(t, strings.Repeat("x", i+1))
		f.step(time.Second)
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d during back-off, want 1", f.calls)
	}
	f.err = nil
	f.out = `[]`
	f.step(agentsFailBackoff)
	if f.calls != 2 {
		t.Fatalf("calls = %d after back-off, want 2", f.calls)
	}
}

func TestAgents_NotificationOnlyWait_Untouched(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.Message = "waiting", "usage limit reached"
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":1,"sessionId":"s1","status":"idle"}]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "waiting" || f.h.posts.Load() != 0 {
		t.Fatalf("state=%s posts=%d; a quota_auto_resume_stale wait must stay", got.State, f.h.posts.Load())
	}
}

func TestAgents_OwnWaitOpensAndCloses(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.Message = "running", "Bash"
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":1,"sessionId":"s1","status":"waiting","waitingFor":"sandbox request"}]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	got := f.readMarker(t, "s1")
	if got.State != "waiting" || got.Message != "sandbox request" || !got.AgentsWait {
		t.Fatalf("marker = %s/%q agents_wait=%v; want an agents-opened wait", got.State, got.Message, got.AgentsWait)
	}
	f.out = `[{"pid":1,"sessionId":"s1","status":"busy"}]`
	f.touchSessions(t, "busy")
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "running" || got.AgentsWait {
		t.Fatalf("marker = %s agents_wait=%v; the watcher must close the wait it opened", got.State, got.AgentsWait)
	}
}

func TestHook_AnyWriteClearsAgentsWait(t *testing.T) {
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	var m marker
	m.Source, m.Tool, m.Session, m.State, m.AgentsWait = "test-mbp", "claude", "abc", "waiting", true
	b, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(h.sessionsDir(), "abc.json"), b, 0o600)
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/r","notification_type":"agent_needs_input","message":"x"}`))
	var got marker
	b, _ = os.ReadFile(filepath.Join(h.sessionsDir(), "abc.json"))
	_ = json.Unmarshal(b, &got)
	if got.AgentsWait {
		t.Fatal("a hook's own wait must not stay marked as agents-opened")
	}
}

func TestHook_StopWithWakingTasks_HoldsAgainstIdle(t *testing.T) {
	h := newHookHarness(t)
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/r","prompt":"go"}`))
	for _, task := range []string{"subagent", "workflow", "teammate", "cloud session"} {
		dispatchHookForTest(t, "stop", []byte(`{"session_id":"abc","cwd":"/r","last_assistant_message":"ok","background_tasks":[{"type":"`+task+`"}]}`))
		var got marker
		b, _ := os.ReadFile(filepath.Join(h.sessionsDir(), "abc.json"))
		_ = json.Unmarshal(b, &got)
		if got.State != "running" || !got.BackgroundWake {
			t.Fatalf("%s: marker %s bg_wake=%v; want running with the hold set", task, got.State, got.BackgroundWake)
		}
		if _, ok := wantFor(got, agentRow{SessionID: "abc", Status: "idle"}); ok {
			t.Fatalf("%s: idle must not end a background-wake hold", task)
		}
	}
	posts := h.posts.Load()
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/r","prompt":"<task-notification>"}`))
	var got marker
	b, _ := os.ReadFile(filepath.Join(h.sessionsDir(), "abc.json"))
	_ = json.Unmarshal(b, &got)
	if got.BackgroundWake {
		t.Fatal("the wake-up turn must clear the hold")
	}
	if h.posts.Load() != posts+1 {
		t.Fatalf("posts = %d, want %d (marking the hold posts nothing)", h.posts.Load(), posts+1)
	}
}

func TestAgents_ResumedWithoutToolName(t *testing.T) {
	f := newAgentsFixture(t)
	m := waitingMarker()
	m.Message = "Claude needs your permission"
	f.writeMarker(t, "abc", m)
	f.out = `[{"pid":1,"sessionId":"abc","status":"busy"}]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "abc"); got.State != "running" || got.ResumedTool != "" || got.ResumedAt == 0 {
		t.Fatalf("marker = %s resumed=%q@%d", got.State, got.ResumedTool, got.ResumedAt)
	}
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/r","notification_type":"permission_prompt","message":"Claude needs your permission to use Edit"}`))
	if got := f.readMarker(t, "abc"); got.State != "running" {
		t.Fatalf("a permission_prompt within the grace re-entered %s", got.State)
	}
	dispatchHookForTest(t, "permission-request", []byte(`{"session_id":"abc","cwd":"/r","tool_name":"Edit","tool_input":{"file_path":"/r/x"}}`))
	if got := f.readMarker(t, "abc"); got.State != "waiting" {
		t.Fatalf("PermissionRequest must still open a wait; state=%s", got.State)
	}
}

func TestApplyCorrection_MarkerChangedSinceSnapshot_NoWrite(t *testing.T) {
	f := newAgentsFixture(t)
	snap := f.writeMarker(t, "s1", waitingMarker())
	var m marker
	m.State, m.Message = "running", "Edit"
	f.writeMarker(t, "s1", m)
	a := activeMarker{sessionID: "s1", markerP: filepath.Join(f.h.sessionsDir(), "s1.json"), lockP: lockPath(f.h.sessionsDir(), "s1"), body: snap, m: waitingMarker()}
	applyCorrection(context.Background(), f.cfg, NewDaemonClient(f.cfg), a, correction{"done", interruptedMessage})
	if got := f.readMarker(t, "s1"); got.State != "running" || got.Message != "Edit" || f.h.posts.Load() != 0 {
		t.Fatalf("marker = %s/%q posts=%d; a write after the snapshot must win", got.State, got.Message, f.h.posts.Load())
	}
}

func TestAgents_StopAfterInterruptedRestoresReply(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.Message = "running", "essay"
	f.writeMarker(t, "abc", m)
	f.out = `[{"pid":1,"sessionId":"abc","status":"idle"}]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	before := f.readMarker(t, "abc")
	if before.State != "done" || before.Message != interruptedMessage {
		t.Fatalf("setup: %s/%q", before.State, before.Message)
	}
	old := hookNow
	hookNow = func() time.Time { return time.Unix(before.StateChangedAt+20, 0) }
	t.Cleanup(func() { hookNow = old })
	dispatchHookForTest(t, "stop", []byte(`{"session_id":"abc","cwd":"/r","last_assistant_message":"Here is the essay.","background_tasks":[]}`))
	got := f.readMarker(t, "abc")
	if got.Message != "Here is the essay." || got.StateChangedAt != before.StateChangedAt {
		t.Fatalf("marker = %q changed_at=%d; want the reply and the original %d", got.Message, got.StateChangedAt, before.StateChangedAt)
	}
}

func TestAgents_UnlistedLiveMarker_SkipsFallback(t *testing.T) {
	f := newAgentsFixture(t)
	orig := ownerAlive
	ownerAlive = func(int, string) bool { return true }
	t.Cleanup(func() { ownerAlive = orig })
	var m marker
	m.State, m.OwnerPID = "running", 4242
	f.writeMarker(t, "nested", m)
	f.out = `[]`
	f.activate(t)
	f.step(time.Second)
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1", f.calls)
	}
	for i := 0; i < 3*int(agentsFallbackEvery/time.Second); i++ {
		f.step(time.Second)
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d; the fallback must skip a pass with only unlisted live sessions", f.calls)
	}
	f.touchSessions(t, "other session flipped")
	f.step(time.Second)
	if f.calls != 2 {
		t.Fatalf("calls = %d; a sessions change still calls", f.calls)
	}
}

func TestAgents_StaleMarkerAtStart_CheckedAtOnce(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.StateChangedAt = "running", f.clock.Add(-10*time.Minute).Unix()
	f.writeMarker(t, "s1", m)
	f.out = `[]`
	f.step(time.Second)
	if f.calls != 1 {
		t.Fatalf("calls = %d; a marker stuck since before the daemon started must be checked at once", f.calls)
	}
}

func TestAgentsLoop_Gating(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.StateChangedAt = "running", f.clock.Add(-10*time.Minute).Unix()
	f.writeMarker(t, "s1", m)
	f.out = `[]`
	enabled := false
	cfg := f.cfg
	var loadErr error
	l := &agentsLoop{w: f.w, enabled: func() bool { return enabled }, load: func() (Config, error) { return cfg, loadErr }}
	tick := func() { f.clock = f.clock.Add(time.Second); l.pass(context.Background()) }
	tick()
	if f.calls != 0 {
		t.Fatal("disabled: no call")
	}
	enabled = true
	for i := 0; i < 8; i++ {
		tick()
	}
	if f.calls != 0 {
		t.Fatal("the setting is re-read only every 10 s")
	}
	tick()
	tick()
	if f.calls != 1 {
		t.Fatalf("calls = %d after the re-read, want 1", f.calls)
	}
	cfg.ServerURL = ""
	for i := 0; i < 70; i++ {
		tick()
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d without a server URL, want 1", f.calls)
	}
}

func writeFakeCLI(t *testing.T, dir, script string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunBounded_GrandchildHoldingStdout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "gc.pid")
	for name, tail := range map[string]string{"exits": "echo '[]'", "hangs": "sleep 20"} {
		t.Run(name, func(t *testing.T) {
			bin := writeFakeCLI(t, filepath.Join(dir, name), "(sleep 20; echo late) &\necho $! > "+pidFile+"\n"+tail)
			start := time.Now()
			_, err := runBounded(context.Background(), 300*time.Millisecond, bin)
			if took := time.Since(start); took > 3*time.Second {
				t.Fatalf("took %v; the call must be bounded by timeout + WaitDelay", took)
			}
			if err == nil {
				t.Fatal("a call that leaves stdout open must fail")
			}
			b, _ := os.ReadFile(pidFile)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			deadline := time.Now().Add(2 * time.Second)
			for pid > 0 && syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if pid > 0 && syscall.Kill(pid, 0) == nil {
				t.Fatalf("grandchild %d survived", pid)
			}
		})
	}
}

func TestResolveClaudeCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pathDir := filepath.Join(home, "pathbin")
	writeFakeCLI(t, pathDir, `echo "9.9.9 (Claude Code)"`)
	t.Setenv("PATH", pathDir+":/usr/bin:/bin")
	local := writeFakeCLI(t, filepath.Join(home, ".local", "bin"), `echo "2.1.287 (Claude Code)"`)
	c := resolveClaudeCLI(context.Background())
	if c.path != local || c.ok || !strings.Contains(c.reason, "older than 2.1.288") {
		t.Fatalf("got %+v; ~/.local/bin wins and an old CLI is refused", c)
	}
	time.Sleep(10 * time.Millisecond)
	writeFakeCLI(t, filepath.Join(home, ".local", "bin"), `echo "2.1.289 (Claude Code)"`)
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(local, future, future)
	if c := resolveClaudeCLI(context.Background()); !c.ok || c.version != "2.1.289" {
		t.Fatalf("got %+v; an updated binary is re-checked", c)
	}
	_ = os.Remove(local)
	if c := resolveClaudeCLI(context.Background()); c.path != filepath.Join(pathDir, "claude") || !c.ok {
		t.Fatalf("got %+v; PATH is next", c)
	}
}

func TestParseClaudeVersion(t *testing.T) {
	v, ok := parseClaudeVersion("2.1.289 (Claude Code)\n")
	if !ok || v != [3]int{2, 1, 289} || !versionAtLeast(v, minAgentsVersion) {
		t.Fatalf("v=%v ok=%v", v, ok)
	}
	if versionAtLeast([3]int{2, 0, 999}, minAgentsVersion) || !versionAtLeast([3]int{3, 0, 0}, minAgentsVersion) {
		t.Fatal("ordering")
	}
	if _, ok := parseClaudeVersion("Usage: claude"); ok {
		t.Fatal("no version")
	}
}
