package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHook_SendsProjectFromCwdBasename(t *testing.T) {
	h := newHookHarness(t)
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/Users/x/Github/learn-language","prompt":"hi"}`))
	if h.posts.Load() != 1 || !strings.Contains((*h.bodies)[0], `"project":"learn-language"`) {
		t.Fatalf("posts=%d body=%v; want project learn-language", h.posts.Load(), *h.bodies)
	}
}

func TestTick_HeartbeatKeepsTheHookProject(t *testing.T) {
	h := newHookHarness(t)
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/srv/ember","prompt":"hi"}`))
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 2 || !strings.Contains((*h.bodies)[1], `"project":"ember"`) {
		t.Fatalf("posts=%d bodies=%v; heartbeat must re-send the project", h.posts.Load(), *h.bodies)
	}
}

func TestHook_NotificationWithoutCwdKeepsTheMarkerProject(t *testing.T) {
	h := newHookHarness(t)
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/srv/ember","prompt":"hi"}`))
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","notification_type":"agent_needs_input","message":"m"}`))
	if h.posts.Load() != 2 || !strings.Contains((*h.bodies)[1], `"project":"ember"`) {
		t.Fatalf("bodies=%v; an event without cwd must keep the stored project", *h.bodies)
	}
}

func TestHook_SendProjectOffSendsNoProject(t *testing.T) {
	h := newHookHarness(t)
	env := filepath.Join(h.home, ".config", "ember", "producer.env")
	b, _ := os.ReadFile(env)
	if err := os.WriteFile(env, append(b, []byte("EMBER_SEND_PROJECT=0\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/srv/ember","prompt":"hi"}`))
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	for _, body := range *h.bodies {
		if strings.Contains(body, `"project"`) {
			t.Fatalf("EMBER_SEND_PROJECT=0 must send none; body %s", body)
		}
	}
}

func setHookNowPtr(t *testing.T, at *time.Time) {
	t.Helper()
	orig := hookNow
	hookNow = func() time.Time { return *at }
	t.Cleanup(func() { hookNow = orig })
}

func writeTickMarker(t *testing.T, h *hookHarness, id string, m marker) string {
	t.Helper()
	if err := os.MkdirAll(h.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	m.Source, m.Tool, m.Session = "test-mbp", "claude", id
	b, _ := json.Marshal(m)
	p := filepath.Join(h.sessionsDir(), id+".json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readTickMarker(t *testing.T, p string) marker {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTick_WaitingWithoutFreshSignalFor30MinIsDeletedNotDone(t *testing.T) {
	h := newHookHarness(t)
	now := time.Now()
	setHookNowPtr(t, &now)
	var m marker
	m.State, m.Message = "waiting", "needs input"
	m.HookAt = now.Add(-29 * time.Minute).Unix()
	m.StateChangedAt = m.HookAt
	p := writeTickMarker(t, h, "abc", m)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 || h.deletes.Load() != 0 {
		t.Fatalf("posts=%d deletes=%d; a 29 min wait is still re-posted", h.posts.Load(), h.deletes.Load())
	}
	now = now.Add(2 * time.Minute)
	dispatchTick(context.Background(), cfg)
	if h.deletes.Load() != 1 || h.posts.Load() != 1 {
		t.Fatalf("posts=%d deletes=%d; the heartbeat is no fresh signal, so 31 min must DELETE and never post", h.posts.Load(), h.deletes.Load())
	}
	for _, b := range *h.bodies {
		if strings.Contains(b, `"state":"done"`) {
			t.Fatalf("the backstop must not post done: %s", b)
		}
	}
	got := readTickMarker(t, p)
	if got.State == "waiting" || !got.WaitExpired {
		t.Fatalf("marker = %s expired=%v; want it parked", got.State, got.WaitExpired)
	}
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 || h.deletes.Load() != 1 {
		t.Fatalf("posts=%d deletes=%d; a parked marker must stay quiet", h.posts.Load(), h.deletes.Load())
	}
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/srv/ember","notification_type":"permission_prompt","message":"approve"}`))
	if h.posts.Load() != 2 || !strings.Contains((*h.bodies)[len(*h.bodies)-1], `"state":"waiting"`) {
		t.Fatalf("bodies=%v; the next hook must re-add the session", *h.bodies)
	}
	if got := readTickMarker(t, p); got.WaitExpired {
		t.Fatal("a hook write must clear the parked flag")
	}
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 3 {
		t.Fatalf("posts=%d; the heartbeat resumes after a hook", h.posts.Load())
	}
}

func TestTick_RunningMarkerIsNotExpiredByTheWaitBackstop(t *testing.T) {
	h := newHookHarness(t)
	now := time.Now()
	setHookNowPtr(t, &now)
	var m marker
	m.State = "running"
	m.HookAt = now.Add(-2 * time.Hour).Unix()
	writeTickMarker(t, h, "abc", m)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 1 || h.deletes.Load() != 0 {
		t.Fatalf("posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
}

func agentInputMarker() marker {
	var m marker
	m.State, m.Message = "waiting", "Agent needs input"
	m.AgentInputWait = true
	return m
}

func TestHook_AgentNeedsInputFlagsTheWait(t *testing.T) {
	h := newHookHarness(t)
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/r","notification_type":"agent_needs_input","message":"m"}`))
	if got := readTickMarker(t, filepath.Join(h.sessionsDir(), "abc.json")); !got.AgentInputWait || got.State != "waiting" {
		t.Fatalf("marker = %+v", got)
	}
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/r","notification_type":"permission_prompt","message":"m"}`))
	if got := readTickMarker(t, filepath.Join(h.sessionsDir(), "abc.json")); got.AgentInputWait {
		t.Fatal("another hook must clear the flag")
	}
}

func TestAgents_AgentInputWaitClearsOnceNoBackgroundAgentWaits(t *testing.T) {
	cases := []struct {
		name, parent string
		want         string
	}{
		{"parent idle", "idle", "idle"},
		{"parent busy", "busy", "running"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAgentsFixture(t)
			f.writeMarker(t, "s1", agentInputMarker())
			f.out = `[{"pid":1,"sessionId":"s1","kind":"interactive","status":"` + c.parent + `"},
			  {"pid":2,"sessionId":"bg1","kind":"background","status":"idle"}]`
			f.activate(t)
			f.step(time.Second)
			f.step(2 * time.Second)
			got := f.readMarker(t, "s1")
			if got.State != c.want || got.AgentInputWait {
				t.Fatalf("state=%s flag=%v; want %s", got.State, got.AgentInputWait, c.want)
			}
			if f.h.posts.Load() != 1 || strings.Contains((*f.h.bodies)[0], `"state":"done"`) {
				t.Fatalf("posts=%d bodies=%v", f.h.posts.Load(), *f.h.bodies)
			}
		})
	}
}

func TestAgents_AgentInputWaitStaysWhileABackgroundAgentWaits(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", agentInputMarker())
	f.out = `[{"pid":1,"sessionId":"s1","kind":"interactive","status":"idle"},
	  {"pid":2,"sessionId":"bg1","kind":"background","status":"waiting","waitingFor":"input"}]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "waiting" || f.h.posts.Load() != 0 {
		t.Fatalf("state=%s posts=%d; a waiting background agent keeps the wait", got.State, f.h.posts.Load())
	}
}
