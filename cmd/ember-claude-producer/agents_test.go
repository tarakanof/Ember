package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type agentsFixture struct {
	h     *hookHarness
	w     *agentsWatcher
	cfg   Config
	clock time.Time
	out   string
	err   error
	calls int
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

// activate runs the pass that sees the marker become active (a baseline, no
// call), then changes ~/.claude/sessions so the next pass calls the CLI.
func (f *agentsFixture) activate(t *testing.T) {
	t.Helper()
	f.step(time.Second)
	if f.calls != 0 {
		t.Fatalf("calls = %d; becoming active must not call the CLI by itself", f.calls)
	}
	f.touchSessions(t, f.clock.String())
}

// touchSessions changes ~/.claude/sessions the way Claude does on a status change.
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
	cases := []struct {
		marker, status, waitingFor string
		want                       correction
		ok                         bool
	}{
		{"waiting", "busy", "", correction{"running", "Bash"}, true},
		{"running", "waiting", "input needed", correction{"waiting", "input needed"}, true},
		{"running", "idle", "", correction{"done", interruptedMessage}, true},
		{"waiting", "idle", "", correction{"done", interruptedMessage}, true},
		{"running", "busy", "", correction{}, false},
		{"waiting:Claude needs your permission", "busy", "", correction{"running", "resumed"}, true},
		{"waiting", "waiting", "permission prompt", correction{}, false},
	}
	for _, c := range cases {
		var m marker
		m.State, m.Message = c.marker, "approve Bash"
		if st, msg, ok := strings.Cut(c.marker, ":"); ok {
			m.State, m.Message = st, msg
		}
		got, ok := wantFor(m, agentRow{SessionID: "s", Status: c.status, WaitingFor: c.waitingFor})
		if ok != c.ok || got != c.want {
			t.Errorf("%s/%s: got %+v,%v want %+v,%v", c.marker, c.status, got, ok, c.want, c.ok)
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
	f.step(time.Second) // becomes active: baseline only
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
	f.step(time.Second) // confirmation due at 1.5 s
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

// A hook that lands between the two snapshots (Stop just before the status
// goes idle) wins: the correction is dropped.
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
