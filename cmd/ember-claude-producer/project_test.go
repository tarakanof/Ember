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

const agentsCwd = "/w/app"

func agentInputMarker() marker {
	var m marker
	m.State, m.Message = "waiting", "Agent needs input"
	m.AgentInputWait = true
	m.Cwd = agentsCwd
	return m
}

func parentRow(status string) string {
	return `{"cwd":"` + agentsCwd + `","kind":"interactive","name":"parent","pid":1,"sessionId":"s1","startedAt":1,"status":"` + status + `"}`
}

func backgroundRow(cwd, state string) string {
	return `{"cwd":"` + cwd + `","id":"bg000001","kind":"background","name":"helper","pid":2,"sessionId":"bg000001-0000","startedAt":1,"state":"` + state + `","status":"idle"}`
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
			f.out = `[` + parentRow(c.parent) + `,` + backgroundRow(agentsCwd, "done") + `]`
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

func TestAgents_AgentInputWaitStaysWhileItsBackgroundAgentIsBlocked(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", agentInputMarker())
	f.out = `[` + parentRow("idle") + `,` + backgroundRow(agentsCwd, "blocked") + `]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "waiting" || f.h.posts.Load() != 0 {
		t.Fatalf("state=%s posts=%d; a blocked background agent in the same cwd keeps the wait", got.State, f.h.posts.Load())
	}
}

func TestAgents_ABlockedAgentOfAnotherProjectDoesNotHoldTheWait(t *testing.T) {
	f := newAgentsFixture(t)
	f.writeMarker(t, "s1", agentInputMarker())
	f.out = `[` + parentRow("idle") + `,` + backgroundRow("/w/other", "blocked") + `]`
	f.activate(t)
	f.step(time.Second)
	f.step(2 * time.Second)
	if got := f.readMarker(t, "s1"); got.State != "idle" {
		t.Fatalf("state=%s; another session's blocked agent must not keep this wait", got.State)
	}
}

func TestAgents_PermissionThenAgentInputStaysWhileTheAgentIsBlocked(t *testing.T) {
	for _, parent := range []string{"idle", "busy"} {
		t.Run(parent, func(t *testing.T) {
			f := newAgentsFixture(t)
			m := agentInputMarker()
			m.PendingPermission = "Bash:abc"
			f.writeMarker(t, "s1", m)
			f.out = `[` + parentRow(parent) + `,` + backgroundRow(agentsCwd, "blocked") + `]`
			f.activate(t)
			f.step(time.Second)
			f.step(2 * time.Second)
			if got := f.readMarker(t, "s1"); got.State != "waiting" || f.h.posts.Load() != 0 {
				t.Fatalf("state=%s posts=%d; the pending permission must not clear a wait its agent still holds", got.State, f.h.posts.Load())
			}
		})
	}
}

func TestAgents_ConfirmedWaitDefersTheBackstop(t *testing.T) {
	f := newAgentsFixture(t)
	now := f.clock
	setHookNowPtr(t, &now)
	m := agentInputMarker()
	m.HookAt = now.Add(-25 * time.Minute).Unix()
	m.StateChangedAt = m.HookAt
	f.writeMarker(t, "s1", m)
	f.out = `[` + parentRow("idle") + `,` + backgroundRow(agentsCwd, "blocked") + `]`
	f.step(time.Second)
	if f.calls != 1 {
		t.Fatalf("calls=%d; a stale wait polls at once", f.calls)
	}
	if got := f.readMarker(t, "s1"); got.WaitConfirmedAt == 0 {
		t.Fatal("a poll that sees the blocked agent must confirm the wait")
	}
	now = now.Add(10 * time.Minute)
	dispatchTick(context.Background(), f.cfg)
	if f.h.deletes.Load() != 0 {
		t.Fatalf("deletes=%d; a wait the poller confirmed 10 min ago must not expire", f.h.deletes.Load())
	}
}

func TestTick_PermissionWaitIsNeverExpiredByTheBackstop(t *testing.T) {
	h := newHookHarness(t)
	now := time.Now()
	setHookNowPtr(t, &now)
	var m marker
	m.State, m.Message = "waiting", "approve Bash"
	m.PendingPermission = "Bash:abc"
	m.HookAt = now.Add(-2 * time.Hour).Unix()
	m.StateChangedAt = m.HookAt
	writeTickMarker(t, h, "abc", m)
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if h.deletes.Load() != 0 || h.posts.Load() != 1 {
		t.Fatalf("posts=%d deletes=%d; the poller confirms permission waits, so the backstop leaves them", h.posts.Load(), h.deletes.Load())
	}
}

type sessionServer struct {
	mu       sync.Mutex
	present  map[string]string
	onPost   func()
	onDelete func()
}

func (s *sessionServer) state(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.present[id]
	return v, ok
}

func useSessionServer(t *testing.T, h *hookHarness) *sessionServer {
	t.Helper()
	s := &sessionServer{present: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req StatusRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch r.Method {
		case http.MethodPost:
			if f := s.onPost; f != nil {
				s.onPost = nil
				f()
			}
			s.mu.Lock()
			s.present[req.Session] = req.State
			s.mu.Unlock()
		case http.MethodDelete:
			if f := s.onDelete; f != nil {
				s.onDelete = nil
				f()
			}
			s.mu.Lock()
			delete(s.present, req.Session)
			s.mu.Unlock()
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)
	env := filepath.Join(h.home, ".config", "ember", "producer.env")
	if err := os.WriteFile(env, []byte("EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL="+srv.URL+"\nEMBER_TOKEN=tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return s
}

func expirableWait(now time.Time) marker {
	var m marker
	m.State, m.Message = "waiting", "needs input"
	m.HookAt = now.Add(-31 * time.Minute).Unix()
	m.StateChangedAt = m.HookAt
	return m
}

func TestExpiry_InFlightHeartbeatLandingAfterTheDeleteIsDeletedAgain(t *testing.T) {
	h := newHookHarness(t)
	srv := useSessionServer(t, h)
	now := time.Now()
	setHookNowPtr(t, &now)
	p := writeTickMarker(t, h, "abc", expirableWait(now))
	body, _ := os.ReadFile(p)
	cfg, _ := loadConfig()
	client := NewDaemonClient(cfg)
	lockP := lockPath(h.sessionsDir(), "abc")
	srv.onPost = func() { expireWait(context.Background(), cfg, client, p, lockP) }
	_ = postReconciled(context.Background(), cfg, client, p, lockP, body, -1)
	if st, ok := srv.state("abc"); ok {
		t.Fatalf("server still has the session (%s); a POST that lands after the expiry must be deleted again", st)
	}
}

func TestExpiry_FreshHookBeforeTheDeleteLandsIsPostedAgain(t *testing.T) {
	h := newHookHarness(t)
	srv := useSessionServer(t, h)
	now := time.Now()
	setHookNowPtr(t, &now)
	writeTickMarker(t, h, "abc", expirableWait(now))
	srv.onDelete = func() {
		dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","cwd":"/srv/ember","notification_type":"permission_prompt","message":"approve"}`))
	}
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	if st, ok := srv.state("abc"); !ok || st != "waiting" {
		t.Fatalf("server state = %q %v; the old expiry's DELETE must not remove a fresh wait", st, ok)
	}
}

func TestHook_MarkerKeepsTheCwdForBackgroundMatching(t *testing.T) {
	h := newHookHarness(t)
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"abc","cwd":"/w/app","prompt":"hi"}`))
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"abc","notification_type":"agent_needs_input","message":"m"}`))
	if got := readTickMarker(t, filepath.Join(h.sessionsDir(), "abc.json")); got.Cwd != "/w/app" {
		t.Fatalf("cwd = %q", got.Cwd)
	}
}
