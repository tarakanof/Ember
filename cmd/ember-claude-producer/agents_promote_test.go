package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Hookless sessions (#285): a session that started before the ember plugin
// was installed, or lost its settings.json hooks to `configure`, reports
// nothing, so its marker stays done while `claude agents` says busy.

const promotePID = 4242

func doneMarker(f *agentsFixture) marker {
	var m marker
	m.State, m.Message, m.Activity = "done", "all set", "Bash: go test"
	m.OwnerPID = promotePID
	m.StateChangedAt = f.clock.Add(-time.Hour).Unix()
	m.HookAt = m.StateChangedAt
	return m
}

// setClaudeStatus rewrites ~/.claude/sessions/<pid>.json like Claude does on
// a status flip, the flip happening now (f.clock), with a distinct mtime.
func (f *agentsFixture) setClaudeStatus(t *testing.T, status string) {
	t.Helper()
	f.setClaudeStatusAt(t, status, f.clock)
}

func (f *agentsFixture) setClaudeStatusAt(t *testing.T, status string, at time.Time) {
	t.Helper()
	p := filepath.Join(f.h.home, ".claude", "sessions", "4242.json")
	b := []byte(`{"pid":4242,"sessionId":"s1","status":"` + status + `","statusUpdatedAt":` + strconv.FormatInt(at.UnixMilli(), 10) + `}`)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// Each flip gets its own mtime, however fast the test runs.
	f.sessionsMtime = f.sessionsMtime.Add(time.Second)
	mt := time.Unix(1_700_000_000, 0).Add(f.sessionsMtime.Sub(time.Time{}))
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func TestWantFor_Dormant(t *testing.T) {
	plain := func(state string) marker {
		var m marker
		m.State = state
		return m
	}
	run := plain("running")
	run.AgentsRun = true
	cases := []struct {
		name   string
		m      marker
		status string
		want   correction
		ok     bool
	}{
		{"done but busy", plain("done"), "busy", correction{"running", workingMessage}, true},
		{"idle but busy", plain("idle"), "busy", correction{"running", workingMessage}, true},
		{"done and idle agree", plain("done"), "idle", correction{}, false},
		{"done and waiting: busy opens the run first", plain("done"), "waiting", correction{}, false},
		{"error is the hooks'", plain("error"), "busy", correction{}, false},
		{"agents run ends plainly", run, "idle", correction{"done", "done"}, true},
	}
	for _, c := range cases {
		got, ok := wantFor(c.m, agentRow{SessionID: "s", Status: c.status})
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v,%v want %+v,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestAgents_DoneButBusy_PromotedAfterConfirmation(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", doneMarker(f))
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	if f.calls != 1 {
		t.Fatalf("calls = %d; a dormant marker's session flipping to busy must call the CLI", f.calls)
	}
	if got := f.readMarker(t, "s1"); got.State != "done" || f.h.posts.Load() != 0 {
		t.Fatalf("one snapshot must not promote; state=%s posts=%d", got.State, f.h.posts.Load())
	}
	f.step(time.Second)
	f.step(time.Second)
	got := f.readMarker(t, "s1")
	if got.State != "running" || got.Message != workingMessage || got.Activity != workingMessage || !got.AgentsRun {
		t.Fatalf("marker = %s/%q/%q agents_run=%v; want running/working opened by the watcher", got.State, got.Message, got.Activity, got.AgentsRun)
	}
	if got.StateChangedAt != hookNow().Unix() && time.Since(time.Unix(got.StateChangedAt, 0)) > time.Minute {
		t.Fatalf("state_changed_at = %d; the promotion must restart the state clock", got.StateChangedAt)
	}
	if f.h.posts.Load() != 1 || !strings.Contains((*f.h.bodies)[0], `"state":"running"`) {
		t.Fatalf("posts=%d bodies=%v; want one running POST", f.h.posts.Load(), *f.h.bodies)
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 (snapshot + confirmation)", f.calls)
	}
}

// The statusline rewrites the marker on every assistant message of a busy
// session; that must not reset the confirmation.
func TestAgents_DoneButBusy_StatuslineWritesDoNotBlock(t *testing.T) {
	f := newAgentsFixture(t)
	m := doneMarker(f)
	f.writeMarker(t, "s1", m)
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	pct := 41
	m.ContextPct, m.StatuslineChangedMs, m.StatuslineAt = &pct, f.clock.UnixMilli(), f.clock.Unix()
	f.writeMarker(t, "s1", m)
	f.step(time.Second)
	f.step(time.Second)
	got := f.readMarker(t, "s1")
	if got.State != "running" {
		t.Fatalf("state = %s; a statusline write between snapshots must not block the promotion", got.State)
	}
	if got.ContextPct == nil || *got.ContextPct != 41 || got.StatuslineAt != m.StatuslineAt {
		t.Fatalf("the promotion must keep the statusline's latest figures: %+v", got)
	}
}

// A hook that lands between snapshots (UserPromptSubmit racing the flip)
// wins, as with every other correction.
func TestAgents_DoneButBusy_HookBetweenSnapshotsWins(t *testing.T) {
	f := newAgentsFixture(t)
	m := doneMarker(f)
	f.writeMarker(t, "s1", m)
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	m.State, m.Message, m.HookAt = "running", "Bash", f.clock.Unix()
	f.writeMarker(t, "s1", m)
	f.step(time.Second)
	f.step(time.Second)
	if got := f.readMarker(t, "s1"); got.Message != "Bash" || got.AgentsRun {
		t.Fatalf("marker = %+v; the hook's running must stand", got)
	}
}

func TestAgents_DoneButBusy_OneOffBusyNotApplied(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", doneMarker(f))
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	f.out = `[{"pid":4242,"sessionId":"s1","status":"idle"}]`
	f.step(2 * time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "done" || f.h.posts.Load() != 0 {
		t.Fatalf("state=%s posts=%d; a busy that didn't last must not promote", got.State, f.h.posts.Load())
	}
}

// A healthy session ending its turn flips its file to idle: no CLI call.
// A quiet session costs nothing.
func TestAgents_Dormant_IdleFlipOrQuiet_NoCall(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", doneMarker(f))
	f.setClaudeStatus(t, "idle")
	for i := 0; i < 90; i++ {
		f.step(time.Second)
	}
	f.setClaudeStatus(t, "idle")
	f.step(time.Second)
	if f.calls != 0 {
		t.Fatalf("calls = %d; idle flips and quiet sessions must not call the CLI", f.calls)
	}
}

// A promoted run follows the session: busy -> idle ends it as done (not
// "interrupted"), and the next busy flip promotes again.
func TestAgents_PromotedRunEndsAndReopens(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", doneMarker(f))
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "running" {
		t.Fatalf("state = %s, want running", got.State)
	}
	f.setClaudeStatus(t, "idle")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"idle"}]`
	f.step(time.Second)
	f.step(2 * time.Second)
	got := f.readMarker(t, "s1")
	if got.State != "done" || got.Message != "done" || got.AgentsRun {
		t.Fatalf("marker = %s/%q agents_run=%v; want done/done", got.State, got.Message, got.AgentsRun)
	}
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "running" {
		t.Fatalf("state = %s; the next busy turn must promote again", got.State)
	}
}

// A file without statusUpdatedAt (an older Claude) falls back to the
// statusline: a change >=5 s after the marker went done is the proof.
func TestAgents_Dormant_StatuslineFallback(t *testing.T) {
	f := newAgentsFixture(t)
	m := doneMarker(f)
	m.StatuslineChangedMs = time.Unix(m.StateChangedAt, 0).Add(time.Second).UnixMilli()
	f.writeMarker(t, "s1", m)
	p := filepath.Join(f.h.home, ".claude", "sessions", "4242.json")
	if err := os.WriteFile(p, []byte(`{"pid":4242,"status":"busy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	if f.calls != 0 {
		t.Fatalf("calls = %d; the statusline re-rendering right after Stop is not a turn", f.calls)
	}
	m.StatuslineChangedMs = f.clock.UnixMilli()
	f.writeMarker(t, "s1", m)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "running" {
		t.Fatalf("state = %s; a later statusline change with a busy file must promote", got.State)
	}
}

// Nested and SDK sessions have no sessions file: never promoted, and their
// statusline changes cost no CLI call.
func TestAgents_Dormant_NoSessionsFile_NoCall(t *testing.T) {
	f := newAgentsFixture(t)
	m := doneMarker(f)
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	for i := 0; i < 5; i++ {
		m.StatuslineChangedMs = f.clock.UnixMilli()
		f.writeMarker(t, "s1", m)
		f.step(2 * time.Second)
	}
	if f.calls != 0 {
		t.Fatalf("calls = %d, want 0 without a sessions file", f.calls)
	}
}

// A slow Stop hook of another plugin keeps the session busy past the
// confirmation window after our Stop marked it done; the busy began before
// the done, so it is the old turn: no call, no flap.
func TestAgents_SlowStop_NoPromotion(t *testing.T) {
	f := newAgentsFixture(t)
	var m marker
	m.State, m.Message, m.OwnerPID = "running", "Bash", promotePID
	m.StateChangedAt = f.clock.Unix()
	f.writeMarker(t, "s1", m)
	turnStart := f.clock
	f.setClaudeStatusAt(t, "busy", turnStart)
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	f.step(time.Second)
	calls := f.calls
	f.clock = f.clock.Add(1500 * time.Millisecond)
	m.State, m.Message, m.StateChangedAt = "done", "all set", f.clock.Unix()
	f.writeMarker(t, "s1", m)
	for i := 0; i < 4; i++ {
		// The statusline keeps writing (a trigger), and Claude rewrites
		// the file with the same busy: neither proves a new turn.
		m.StatuslineChangedMs = f.clock.UnixMilli()
		f.writeMarker(t, "s1", m)
		f.setClaudeStatusAt(t, "busy", turnStart)
		f.step(time.Second)
	}
	f.setClaudeStatus(t, "idle")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"idle"}]`
	f.step(time.Second)
	f.step(time.Second)
	if got := f.readMarker(t, "s1"); got.State != "done" || got.Message != "all set" || f.h.posts.Load() != 0 {
		t.Fatalf("marker = %s/%q posts=%d; a lingering busy must not promote", got.State, got.Message, f.h.posts.Load())
	}
	if f.calls != calls {
		t.Fatalf("calls = %d after the turn end, want %d: a healthy turn end costs no CLI call", f.calls, calls)
	}
}

// Same second: the busy began just before the Stop, inside the second
// state_changed_at truncates to.
func TestBusyAfterDormant(t *testing.T) {
	var m marker
	m.State, m.StateChangedAt = "done", 1_800_000_000
	cases := []struct {
		name string
		s    claudeSession
		want bool
	}{
		{"busy after done", claudeSession{"busy", 1_800_000_005_000}, true},
		{"busy in the done second", claudeSession{"busy", 1_800_000_000_900}, false},
		{"busy before done (slow Stop)", claudeSession{"busy", 1_799_999_998_000}, false},
		{"idle with a background shell", claudeSession{"shell", 1_800_000_005_000}, false},
		{"idle", claudeSession{"idle", 1_800_000_005_000}, false},
		{"no statusUpdatedAt, no statusline", claudeSession{"busy", 0}, false},
	}
	for _, c := range cases {
		if got := busyAfterDormant(m, c.s); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Measured (2.1.289): after Stop with a background Bash running, the file
// says "shell" while `claude agents` says busy. A done marker stays done,
// and a run the watcher opened ends.
func TestAgents_BackgroundShell_NeverWorking(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", doneMarker(f))
	f.setClaudeStatus(t, "shell")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	for i := 0; i < 5; i++ {
		f.step(time.Second)
	}
	if got := f.readMarker(t, "s1"); got.State != "done" || f.calls != 0 {
		t.Fatalf("state=%s calls=%d; idle with a background shell must stay done", got.State, f.calls)
	}

	g := newAgentsFixture(t)
	g.writeMarker(t, "s1", doneMarker(g))
	g.setClaudeStatus(t, "busy")
	g.out = `[{"pid":4242,"sessionId":"s1","status":"busy"}]`
	g.step(time.Second)
	g.step(2 * time.Second)
	if got := g.readMarker(t, "s1"); got.State != "running" || !got.AgentsRun {
		t.Fatalf("setup: state = %s, want an agents run", got.State)
	}
	g.setClaudeStatus(t, "shell")
	g.step(time.Second)
	g.step(2 * time.Second)
	if got := g.readMarker(t, "s1"); got.State != "done" || got.AgentsRun {
		t.Fatalf("marker = %s agents_run=%v; a background shell must end the agents run", got.State, got.AgentsRun)
	}
}

// Two markers on one owner pid each see the flip.
func TestAgents_SharedOwnerPID_BothWake(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", doneMarker(f))
	f.writeMarker(t, "s2", doneMarker(f))
	f.setClaudeStatus(t, "busy")
	f.out = `[{"pid":4242,"sessionId":"s1","status":"busy"},{"pid":4242,"sessionId":"s2","status":"busy"}]`
	f.step(time.Second)
	f.step(2 * time.Second)
	if a, b := f.readMarker(t, "s1"), f.readMarker(t, "s2"); a.State != "running" || b.State != "running" {
		t.Fatalf("states = %s,%s; both markers must be promoted", a.State, b.State)
	}
}

func TestHook_WriteClearsAgentsRunAndStampsHookAt(t *testing.T) {
	h := newHookHarness(t)
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	var m marker
	m.Source, m.Tool, m.Session, m.State, m.AgentsRun = "test-mbp", "claude", "abc", "running", true
	m.StatuslineAt = 1_700_000_000
	b, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(h.sessionsDir(), "abc.json"), b, 0o600)
	dispatchHookForTest(t, "stop", []byte(`{"session_id":"abc","cwd":"/r","last_assistant_message":"ok"}`))
	var got marker
	b, _ = os.ReadFile(filepath.Join(h.sessionsDir(), "abc.json"))
	_ = json.Unmarshal(b, &got)
	if got.AgentsRun || got.HookAt == 0 || got.StatuslineAt != 1_700_000_000 {
		t.Fatalf("marker = %+v; a hook write must clear agents_run, stamp hook_at and keep statusline_at", got)
	}
}

func TestStaleHookSessions(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	mk := func(id string, hookAgo, slAgo time.Duration) marker {
		var m marker
		m.Session, m.State = id, "done"
		m.HookAt = now.Add(-hookAgo).Unix()
		m.StatuslineAt = now.Add(-slAgo).Unix()
		return m
	}
	legacy := mk("legacy", 0, time.Minute)
	legacy.HookAt, legacy.StateChangedAt = 0, now.Add(-3*time.Hour).Unix()
	rows := map[string]agentRow{
		"stale":   {PID: 1, SessionID: "stale", Status: "busy"},
		"fresh":   {PID: 2, SessionID: "fresh", Status: "busy"},
		"idle":    {PID: 3, SessionID: "idle", Status: "idle"},
		"gone":    {PID: 4, SessionID: "gone", Status: "busy"},
		"legacy":  {PID: 5, SessionID: "legacy", Status: "busy"},
		"nostats": {PID: 6, SessionID: "nostats", Status: "busy"},
	}
	markers := []marker{
		mk("stale", 2*time.Hour, time.Minute),
		mk("fresh", time.Minute, 0),
		mk("idle", 2*time.Hour, time.Minute),
		mk("gone", 2*time.Hour, time.Hour), // statusline quiet too
		legacy,
		mk("nostats", 2*time.Hour, 0),
	}
	markers[5].StatuslineAt = 0
	got := staleHookSessions(markers, rows, now)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.sessionID)
	}
	if strings.Join(ids, ",") != "stale" {
		t.Fatalf("stale = %v; want stale (no hook_at: skipped)", ids)
	}
	if got[0].pid != 1 || got[0].hookAge != 2*time.Hour {
		t.Fatalf("got %+v", got[0])
	}
}

func TestHook_ToolOutcomeAndBackgroundWakeClearAgentsRun(t *testing.T) {
	for _, c := range []struct{ event, body string }{
		{"post-tool-use", `{"session_id":"abc","cwd":"/r","tool_name":"Bash","tool_input":{"command":"ls"}}`},
		{"stop", `{"session_id":"abc","cwd":"/r","last_assistant_message":"ok","background_tasks":[{"type":"subagent"}]}`},
	} {
		h := newHookHarness(t)
		if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		var m marker
		m.Source, m.Tool, m.Session, m.State, m.AgentsRun = "test-mbp", "claude", "abc", "running", true
		b, _ := json.Marshal(m)
		_ = os.WriteFile(filepath.Join(h.sessionsDir(), "abc.json"), b, 0o600)
		dispatchHookForTest(t, c.event, []byte(c.body))
		var got marker
		b, _ = os.ReadFile(filepath.Join(h.sessionsDir(), "abc.json"))
		_ = json.Unmarshal(b, &got)
		if got.AgentsRun || got.HookAt == 0 {
			t.Errorf("%s: agents_run=%v hook_at=%d; any hook write must clear agents_run", c.event, got.AgentsRun, got.HookAt)
		}
	}
}
