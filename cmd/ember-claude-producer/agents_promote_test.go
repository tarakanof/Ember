package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
// a status flip (a distinct mtime each call).
func (f *agentsFixture) setClaudeStatus(t *testing.T, status string) {
	t.Helper()
	p := filepath.Join(f.h.home, ".claude", "sessions", "4242.json")
	b := []byte(`{"pid":4242,"sessionId":"s1","status":"` + status + `"}`)
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

// Without a sessions file (no owner pid recorded), a statusline change well
// after the marker went dormant triggers the check; each change once.
func TestAgents_Dormant_StatuslineTrigger(t *testing.T) {
	f := newAgentsFixture(t)
	m := doneMarker(f)
	m.OwnerPID = 0
	m.StatuslineChangedMs = time.Unix(m.StateChangedAt, 0).Add(time.Second).UnixMilli()
	f.writeMarker(t, "s1", m)
	f.out = `[{"pid":4242,"sessionId":"s1","status":"idle"}]`
	f.step(time.Second)
	if f.calls != 0 {
		t.Fatalf("calls = %d; the statusline re-rendering right after Stop is not a turn", f.calls)
	}
	m.StatuslineChangedMs = f.clock.UnixMilli()
	f.writeMarker(t, "s1", m)
	f.step(time.Second)
	f.step(time.Second)
	f.step(time.Second)
	if f.calls != 1 {
		t.Fatalf("calls = %d; a later statusline change must check once", f.calls)
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
