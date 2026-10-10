package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type fakeAppServer struct {
	t    *testing.T
	path string
	ln   net.Listener

	mu             sync.Mutex
	threads        map[string]map[string]any
	loaded         []string
	resumeFailures int
	received       []map[string]any
	conns          []*wsConn
	beforeRead     func(id string)
	hang           bool
}

func shortSockDir(t *testing.T) string {
	t.Helper()
	// t.TempDir paths overflow sun_path (104 bytes on macOS).
	dir, err := os.MkdirTemp("/tmp", "ecas")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newFakeAppServer(t *testing.T, path string) *fakeAppServer {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAppServer{t: t, path: path, ln: ln, threads: map[string]map[string]any{}}
	t.Cleanup(f.close)
	go f.accept()
	return f
}

func (f *fakeAppServer) close() {
	_ = f.ln.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Close()
	}
}

func (f *fakeAppServer) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.serve(c)
	}
}

func (f *fakeAppServer) serve(c net.Conn) {
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.URL.Path != "/rpc" {
		_ = c.Close()
		return
	}
	key := req.Header.Get("Sec-WebSocket-Key")
	_, _ = c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"))
	ws := &wsConn{c: c, br: br}
	f.mu.Lock()
	f.conns = append(f.conns, ws)
	f.mu.Unlock()
	for {
		raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			f.t.Errorf("client sent non-JSON frame %q", raw)
			continue
		}
		f.mu.Lock()
		f.received = append(f.received, m)
		f.mu.Unlock()
		if id, ok := m["id"]; ok && m["method"] != nil {
			f.answer(ws, id, m["method"].(string), m["params"])
		}
	}
}

func (f *fakeAppServer) answer(ws *wsConn, id any, method string, params any) {
	p, _ := params.(map[string]any)
	f.mu.Lock()
	hook := f.beforeRead
	if f.hang {
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()
	if method == "thread/read" && hook != nil {
		hook(p["threadId"].(string))
	}
	f.mu.Lock()
	var result any
	var rpcErr any
	switch method {
	case "initialize":
		result = map[string]any{"userAgent": "codex_app_server_daemon/0.160.0 (Mac OS 27.0.0; arm64)", "codexHome": "/x", "platformFamily": "unix", "platformOs": "macos"}
	case "thread/loaded/list":
		result = map[string]any{"data": f.loaded, "nextCursor": nil}
	case "thread/read":
		if th := f.threads[p["threadId"].(string)]; th != nil {
			result = map[string]any{"thread": th}
		} else {
			rpcErr = map[string]any{"code": -32600, "message": "thread not found"}
		}
	case "thread/resume":
		if f.resumeFailures > 0 {
			f.resumeFailures--
			rpcErr = map[string]any{"code": -32600, "message": "no rollout found for thread id " + p["threadId"].(string)}
		} else {
			result = map[string]any{"thread": f.threads[p["threadId"].(string)]}
		}
	case "thread/unsubscribe":
		result = map[string]any{"status": "unsubscribed"}
	default:
		result = map[string]any{}
	}
	f.mu.Unlock()
	msg := map[string]any{"id": id}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	f.sendOn(ws, msg)
}

func (f *fakeAppServer) sendOn(ws *wsConn, v any) {
	b, _ := json.Marshal(v)
	_ = ws.WriteText(b)
}

func (f *fakeAppServer) send(v any) {
	f.mu.Lock()
	conns := append([]*wsConn(nil), f.conns...)
	f.mu.Unlock()
	for _, c := range conns {
		f.sendOn(c, v)
	}
}

func (f *fakeAppServer) notify(method string, params any) {
	f.send(map[string]any{"method": method, "params": params})
}

func (f *fakeAppServer) addThread(id, source string, status any, extra map[string]any) {
	th := map[string]any{"id": id, "ephemeral": false, "source": source, "originator": "codex-tui",
		"preview": "", "updatedAt": time.Now().Unix(), "status": status, "cwd": "/w", "cliVersion": "0.160.0",
		"createdAt": time.Now().Unix(), "modelProvider": "openai", "projectId": nil, "sessionId": id, "turns": []any{}}
	for k, v := range extra {
		th[k] = v
	}
	f.mu.Lock()
	f.threads[id] = th
	f.mu.Unlock()
}

func (f *fakeAppServer) messages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.received...)
}

func (f *fakeAppServer) calls(method string) []map[string]any {
	var out []map[string]any
	for _, m := range f.messages() {
		if m["method"] == method {
			out = append(out, m)
		}
	}
	return out
}

func active(flags ...string) map[string]any {
	if flags == nil {
		flags = []string{}
	}
	return map[string]any{"type": "active", "activeFlags": flags}
}

var idle = map[string]any{"type": "idle"}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testAppServerConfig(sock string) Config {
	return Config{Common: producer.Common{Source: "mbp", ActivityTrailEnabled: true}, Gauges: producer.Gauges{ContextPctEnabled: true}, ActivityWindowSeconds: 90, RatePctEnabled: true, AppServerEnabled: true, AppServerSocket: sock}
}

func startAppServer(t *testing.T, cfg Config) *appServer {
	t.Helper()
	as := newAppServer(cfg)
	as.pollEvery, as.backoffMin, as.backoffMax, as.retryEvery = 10*time.Millisecond, 10*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { as.run(ctx); close(done) }()
	t.Cleanup(func() { stopAppServer(t, cancel, done) })
	return as
}

func stopAppServer(t *testing.T, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Errorf("app-server loop did not stop within 5s of cancel")
	}
}

func bootstrapped(as *appServer, ids ...string) func() bool {
	return func() bool {
		as.mu.Lock()
		defer as.mu.Unlock()
		if len(as.unread) != 0 {
			return false
		}
		for _, id := range ids {
			if as.threads[id] == nil && as.ephemeral[id].IsZero() {
				return false
			}
		}
		return true
	}
}

func connected(as *appServer) func() bool {
	return func() bool { ok, _ := as.status(); return ok }
}

func subscribed(as *appServer, id string) func() bool {
	return func() bool {
		as.mu.Lock()
		defer as.mu.Unlock()
		th := as.threads[id]
		return th != nil && th.subscribed
	}
}

func postFor(posts []producer.StatusRequest, id string) (producer.StatusRequest, bool) {
	for i := len(posts) - 1; i >= 0; i-- {
		if posts[i].Session == id {
			return posts[i], true
		}
	}
	return producer.StatusRequest{}, false
}

func waitState(t *testing.T, as *appServer, id, want string) producer.StatusRequest {
	t.Helper()
	var last producer.StatusRequest
	waitFor(t, id+" "+want, func() bool {
		as.mu.Lock()
		if th := as.threads[id]; th != nil {
			th.post.Reset()
		}
		as.mu.Unlock()
		if p, ok := postFor(as.tick().posts, id); ok {
			last = p
			return p.State == want
		}
		return false
	})
	return last
}

func TestAppServer_InitializesAsNonOriginatingClient(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	as := startAppServer(t, testAppServerConfig(sock))
	waitFor(t, "connect", connected(as))
	init := f.calls("initialize")
	if len(init) != 1 {
		t.Fatalf("want 1 initialize, got %d", len(init))
	}
	params := init[0]["params"].(map[string]any)
	if name := params["clientInfo"].(map[string]any)["name"]; name != "codex_app_server_daemon" {
		t.Errorf("clientInfo.name = %v", name)
	}
	optOut := params["capabilities"].(map[string]any)["optOutNotificationMethods"].([]any)
	if len(optOut) == 0 {
		t.Error("deltas not opted out")
	}
	waitFor(t, "initialized", func() bool { return len(f.calls("initialized")) == 1 })
	if _, ua := as.status(); !strings.HasPrefix(ua, "codex_app_server_daemon/0.160.0") {
		t.Errorf("user agent = %q", ua)
	}
}

func TestAppServer_BootstrapsLoadedThreadsAndMapsStatus(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t-run", "vscode", active(), nil)
	f.addThread("t-idle", "cli", idle, map[string]any{"preview": "hi"})
	f.addThread("t-new", "cli", idle, nil)
	f.addThread("t-eph", "cli", active(), map[string]any{"ephemeral": true})
	f.loaded = []string{"t-run", "t-idle", "t-new", "t-eph"}
	as := startAppServer(t, testAppServerConfig(sock))
	waitFor(t, "loaded threads read", bootstrapped(as, f.loaded...))
	waitState(t, as, "t-run", "running")
	waitState(t, as, "t-idle", "done")
	eph, hasNew, newState := func() (bool, bool, string) {
		as.mu.Lock()
		defer as.mu.Unlock()
		_, eph := as.threads["t-eph"]
		th := as.threads["t-new"]
		if th == nil {
			return eph, false, ""
		}
		return eph, true, th.d.state
	}()
	if eph {
		t.Error("ephemeral helper thread tracked")
	}
	if !hasNew {
		t.Fatal("loaded thread t-new not tracked")
	}
	if newState != "" {
		t.Errorf("thread without a turn has state %q, want none", newState)
	}

	cases := []struct {
		status any
		want   string
	}{
		{active("waitingOnApproval"), "waiting"},
		{active("waitingOnUserInput"), "waiting"},
		{active(), "running"},
		{idle, "done"},
		{map[string]any{"type": "systemError"}, "error"},
		{idle, "error"},
		{active(), "running"},
	}
	for _, c := range cases {
		f.notify("thread/status/changed", map[string]any{"threadId": "t-run", "status": c.status})
		waitState(t, as, "t-run", c.want)
	}

	f.notify("thread/closed", map[string]any{"threadId": "t-run"})
	waitFor(t, "DELETE on thread/closed", func() bool {
		for _, d := range as.tick().deletes {
			if d.Session == "t-run" && d.Tool == "codex" && d.Source == "mbp" {
				return true
			}
		}
		return false
	})
	if !as.tick().owned["t-run"] {
		t.Error("a closed thread must stay owned so the rollout watcher does not revive it")
	}
}

func TestAppServer_SubscribesOnlyWhileBusyAndRetriesNoRollout(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", idle, nil)
	f.loaded = []string{"t1"}
	f.resumeFailures = 2
	as := startAppServer(t, testAppServerConfig(sock))
	waitFor(t, "connect", connected(as))
	waitFor(t, "bootstrap read", func() bool { return len(f.calls("thread/read")) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(f.calls("thread/resume")); n != 0 {
		t.Fatalf("idle thread resumed %d times; a subscriber keeps it loaded forever", n)
	}

	f.notify("thread/status/changed", map[string]any{"threadId": "t1", "status": active()})
	waitFor(t, "resume retried past no rollout found", func() bool { return len(f.calls("thread/resume")) == 3 })
	r := f.calls("thread/resume")[2]["params"].(map[string]any)
	if r["threadId"] != "t1" || r["excludeTurns"] != true {
		t.Errorf("resume params = %v", r)
	}
	waitFor(t, "subscribed", func() bool {
		as.mu.Lock()
		defer as.mu.Unlock()
		return as.threads["t1"].subscribed
	})

	f.notify("thread/status/changed", map[string]any{"threadId": "t1", "status": idle})
	waitFor(t, "unsubscribe on idle", func() bool { return len(f.calls("thread/unsubscribe")) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(f.calls("thread/resume")); n != 3 {
		t.Errorf("resume calls after idle = %d, want 3", n)
	}
}

func TestAppServer_TurnDetailsFillStatusFields(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", active(), nil)
	f.loaded = []string{"t1"}
	as := startAppServer(t, testAppServerConfig(sock))
	waitState(t, as, "t1", "running")

	f.notify("item/started", map[string]any{"threadId": "t1", "turnId": "u", "startedAtMs": 1,
		"item": map[string]any{"type": "commandExecution", "id": "i1", "command": "/bin/zsh -lc 'go test ./...'", "status": "inProgress", "commandActions": []any{}, "cwd": "/w"}})
	f.notify("item/started", map[string]any{"threadId": "t1", "turnId": "u", "startedAtMs": 2,
		"item": map[string]any{"type": "mcpToolCall", "id": "i2", "server": "gh", "tool": "search", "status": "inProgress"}})
	f.notify("item/completed", map[string]any{"threadId": "t1", "turnId": "u",
		"item": map[string]any{"type": "agentMessage", "id": "i3", "text": "  Tests pass.  "}})
	f.notify("thread/tokenUsage/updated", map[string]any{"threadId": "t1", "turnId": "u",
		"tokenUsage": map[string]any{"last": map[string]any{"inputTokens": 50000}, "total": map[string]any{}, "modelContextWindow": 200000}})
	f.notify("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"limitId": "codex",
		"primary": map[string]any{"usedPercent": 42, "resetsAt": time.Now().Add(time.Hour).Unix(), "windowDurationMins": 300}, "secondary": nil}})

	var p producer.StatusRequest
	waitFor(t, "details", func() bool {
		p = waitState(t, as, "t1", "running")
		return p.Message != "" && p.ContextPct != nil && p.RateWindowPct != nil && strings.Contains(p.Activity, "mcp")
	})
	if want := producer.PrependTrail("mcp: search", "exec: go test ./..."); p.Activity != want {
		t.Errorf("activity = %q, want %q (newest tool first, shell wrapper dropped)", p.Activity, want)
	}
	if p.Message != "Tests pass." || *p.ContextPct != 25 || *p.RateWindowPct != 42 {
		t.Errorf("post = %+v ctx=%d rate=%d", p, *p.ContextPct, *p.RateWindowPct)
	}

	f.notify("turn/completed", map[string]any{"threadId": "t1", "turn": map[string]any{"id": "u", "status": "failed", "items": []any{}}})
	f.notify("thread/status/changed", map[string]any{"threadId": "t1", "status": idle})
	waitState(t, as, "t1", "error")

	f.notify("thread/status/changed", map[string]any{"threadId": "t1", "status": active()})
	waitState(t, as, "t1", "running")
	f.notify("turn/completed", map[string]any{"threadId": "t1", "turn": map[string]any{"id": "v", "status": "interrupted",
		"items": []any{map[string]any{"type": "agentMessage", "id": "m", "text": "Stopped early"}}}})
	f.notify("thread/status/changed", map[string]any{"threadId": "t1", "status": idle})
	if p := waitState(t, as, "t1", "done"); p.Message != "Stopped early" {
		t.Errorf("interrupted turn message = %q", p.Message)
	}
}

func TestAppServer_RespectsSourceAndClaudeFilters(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t-exec", "exec", active(), nil)
	f.addThread("t-claude", "vscode", active(), map[string]any{"originator": "Claude Code"})
	f.addThread("t-sub", "", active(), map[string]any{"source": map[string]any{"subAgent": "review"}})
	f.addThread("t-tui", "vscode", active(), nil)
	f.loaded = []string{"t-exec", "t-claude", "t-sub", "t-tui"}
	as := startAppServer(t, testAppServerConfig(sock))
	waitState(t, as, "t-tui", "running")
	waitFor(t, "t-tui subscribed", subscribed(as, "t-tui"))
	tk := as.tick()
	for _, id := range []string{"t-exec", "t-claude", "t-sub"} {
		if _, ok := postFor(tk.posts, id); ok {
			t.Errorf("%s posted by default", id)
		}
		if !tk.owned[id] {
			t.Errorf("%s not owned: the rollout watcher would apply the same filter twice", id)
		}
	}
	if n := len(f.calls("thread/resume")); n != 1 {
		t.Errorf("resume calls = %d, want only the tracked thread", n)
	}
	as.mu.Lock()
	for _, id := range []string{"t-exec", "t-claude", "t-sub"} {
		if th := as.threads[id]; th == nil || th.tracked {
			t.Errorf("%s tracked or missing: %+v", id, th)
		}
	}
	as.mu.Unlock()

	cfg := testAppServerConfig(sock)
	cfg.Sources = parseSources("cli,vscode,exec")
	cfg.IncludeClaude = true
	as2 := startAppServer(t, cfg)
	waitState(t, as2, "t-exec", "running")
	waitFor(t, "t-claude subscribed", subscribed(as2, "t-claude"))
	f.notify("item/completed", map[string]any{"threadId": "t-claude", "turnId": "u", "item": map[string]any{"type": "agentMessage", "id": "m", "text": "Doing it"}})
	waitFor(t, "via Claude", func() bool {
		p := waitState(t, as2, "t-claude", "running")
		return p.Message == "via Claude: Doing it"
	})
}

func TestAppServer_NeverAnswersServerRequests(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", active("waitingOnApproval"), nil)
	f.loaded = []string{"t1"}
	as := startAppServer(t, testAppServerConfig(sock))
	waitFor(t, "subscribed", func() bool { return len(f.calls("thread/resume")) == 1 })

	requests := []map[string]any{
		{"id": 0, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "t1", "turnId": "u", "itemId": "i", "command": "rm -rf build"}},
		{"id": 1, "method": "item/fileChange/requestApproval", "params": map[string]any{"threadId": "t1", "turnId": "u", "itemId": "j"}},
		{"id": "s-2", "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "t1"}},
		{"id": 3, "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": "t1"}},
		{"id": 4, "method": "mcpServer/elicitation/request", "params": map[string]any{"threadId": "t1"}},
		{"id": 5, "method": "execCommandApproval", "params": map[string]any{"conversationId": "t1"}},
		{"id": 6, "method": "applyPatchApproval", "params": map[string]any{"conversationId": "t1"}},
		{"id": 7, "method": "account/chatgptAuthTokens/refresh", "params": map[string]any{}},
		{"id": 8, "method": "item/tool/call", "params": map[string]any{"threadId": "t1", "tool": "x"}},
		{"id": 9, "method": "currentTime/read", "params": map[string]any{}},
		{"id": 10, "method": "attestation/generate", "params": map[string]any{}},
	}
	for _, r := range requests {
		f.send(r)
	}
	f.notify("serverRequest/resolved", map[string]any{"threadId": "t1", "requestId": 0})
	f.notify("thread/status/changed", map[string]any{"threadId": "t1", "status": idle})
	waitFor(t, "unsubscribe", func() bool { return len(f.calls("thread/unsubscribe")) == 1 })
	waitState(t, as, "t1", "done")

	for _, m := range f.messages() {
		if _, ok := m["result"]; ok {
			t.Errorf("client sent a response: %v", m)
		}
		if _, ok := m["error"]; ok {
			t.Errorf("client sent an error response: %v", m)
		}
		if m["method"] == nil {
			t.Errorf("client sent a message without a method: %v", m)
		}
	}
	for _, m := range f.messages() {
		if id, ok := m["id"].(float64); ok && id < 1 {
			t.Errorf("client request id %v; ids start at 1 and never echo the server's", id)
		}
	}
}

func TestOutbound_CannotExpressAResponse(t *testing.T) {
	typ := reflect.TypeOf(outbound{})
	var tags []string
	for i := 0; i < typ.NumField(); i++ {
		tags = append(tags, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	if !reflect.DeepEqual(tags, []string{"id", "method", "params"}) {
		t.Fatalf("outbound fields = %v; a result/error field would let the client answer approvals", tags)
	}
}

func TestAppServer_AbsentSocketIsANoOp(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "missing.sock")
	as := startAppServer(t, testAppServerConfig(sock))
	time.Sleep(50 * time.Millisecond)
	tk := as.tick()
	if ok, _ := as.status(); ok || len(tk.posts) != 0 || len(tk.owned) != 0 || tk.rate != nil {
		t.Fatalf("absent socket: connected=%v tick=%+v", ok, tk)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("the producer must never create the daemon socket")
	}
}

func TestAppServer_ReconnectsAfterDaemonRestart(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", active(), nil)
	f.loaded = []string{"t1"}
	as := startAppServer(t, testAppServerConfig(sock))
	waitState(t, as, "t1", "running")

	f.close()
	waitFor(t, "disconnect", func() bool { return !connected(as)() })
	tk := as.tick()
	if len(tk.released) != 1 || tk.released[0] != "t1" || tk.owned["t1"] {
		t.Fatalf("after disconnect: released=%v owned=%v", tk.released, tk.owned)
	}
	_ = os.Remove(sock)

	f2 := newFakeAppServer(t, sock)
	f2.addThread("t2", "cli", active(), nil)
	f2.loaded = []string{"t2"}
	var del, posted bool
	waitFor(t, "t2 posted and t1 deleted", func() bool {
		tk := as.tick()
		for _, d := range tk.deletes {
			del = del || d.Session == "t1"
		}
		_, ok := postFor(tk.posts, "t2")
		posted = posted || ok
		return del && posted && tk.owned["t1"]
	})
	if len(f2.calls("initialize")) != 1 {
		t.Error("no initialize on reconnect")
	}
}

func TestAppServer_StatusDuringInFlightReadWins(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", idle, map[string]any{"preview": "hi"})
	f.loaded = []string{"t1"}
	f.beforeRead = func(id string) {
		f.notify("thread/status/changed", map[string]any{"threadId": id, "status": active()})
	}
	as := startAppServer(t, testAppServerConfig(sock))
	waitState(t, as, "t1", "running")
	waitFor(t, "resume", func() bool { return len(f.calls("thread/resume")) == 1 })
}

func TestAppServer_MarksLoadedThreadsPendingUntilTheirRead(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", active(), nil)
	f.loaded = []string{"t1"}
	reading, release := make(chan struct{}), make(chan struct{})
	f.beforeRead = func(string) {
		close(reading)
		<-release
	}
	as := startAppServer(t, testAppServerConfig(sock))
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("timed out waiting for thread/read")
	}
	tk := as.tick()
	close(release)
	if !tk.pending["t1"] || tk.owned["t1"] {
		t.Errorf("awaiting its read: pending=%v owned=%v, want pending and not owned", tk.pending["t1"], tk.owned["t1"])
	}
	waitState(t, as, "t1", "running")
	if tk := as.tick(); tk.pending["t1"] || !tk.owned["t1"] {
		t.Errorf("after its read: pending=%v owned=%v, want owned", tk.pending["t1"], tk.owned["t1"])
	}
}

func TestAppServer_CloseDuringInFlightReadWins(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	f.addThread("t1", "cli", active(), nil)
	f.loaded = []string{"t1"}
	f.beforeRead = func(id string) {
		f.notify("thread/closed", map[string]any{"threadId": id})
	}
	as := startAppServer(t, testAppServerConfig(sock))
	waitFor(t, "read", func() bool { return len(f.calls("thread/read")) == 1 })
	waitFor(t, "read applied", func() bool {
		as.mu.Lock()
		defer as.mu.Unlock()
		return len(as.unread) == 0
	})
	tk := as.tick()
	if len(tk.posts) != 0 || tk.owned["t1"] {
		t.Fatalf("closed thread resurrected by its read: %+v", tk)
	}
}

func TestAppServer_ReconnectsWhenTheDaemonStopsAnswering(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "s.sock")
	f := newFakeAppServer(t, sock)
	as := newAppServer(testAppServerConfig(sock))
	as.pollEvery, as.backoffMin, as.backoffMax = 10*time.Millisecond, 10*time.Millisecond, 20*time.Millisecond
	as.liveEvery, as.liveTimeout = 30*time.Millisecond, 30*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { as.run(ctx); close(done) }()
	t.Cleanup(func() { stopAppServer(t, cancel, done) })
	waitFor(t, "connect", connected(as))
	f.mu.Lock()
	f.hang = true
	f.mu.Unlock()
	waitFor(t, "reconnect attempt", func() bool { return len(f.calls("initialize")) >= 2 })
}

func TestAppServerReport_ShowsSocketDaemonAndUpdater(t *testing.T) {
	dir := shortSockDir(t)
	cfg := testAppServerConfig(filepath.Join(dir, "s.sock"))
	cfg.CodexHome = dir
	ctx := context.Background()
	got := strings.Join(appServerReport(ctx, cfg), "\n")
	if !strings.Contains(got, "socket: absent") || !strings.Contains(got, "updater: n/a") {
		t.Errorf("no daemon:\n%s", got)
	}

	f := newFakeAppServer(t, cfg.AppServerSocket)
	f.loaded = []string{"a", "b"}
	if err := os.MkdirAll(filepath.Join(dir, "app-server-daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = strings.Join(appServerReport(ctx, cfg), "\n")
	for _, want := range []string{"socket: present", `connect: OK, daemon "codex_app_server_daemon/0.160.0`, "2 loaded threads", "updater: on (default"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "app-server-daemon", "settings.json"), []byte(`{"updater":{"autoUpdateEnabled":false}}`), 0o600)
	if got := strings.Join(appServerReport(ctx, cfg), "\n"); !strings.Contains(got, "updater: off") {
		t.Errorf("updater off not read:\n%s", got)
	}
	for _, m := range f.messages() {
		if _, ok := m["result"]; ok {
			t.Errorf("doctor sent a response: %v", m)
		}
	}
}
