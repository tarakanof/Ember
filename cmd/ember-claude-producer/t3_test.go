package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type t3FakeProc struct {
	ppid int
	comm string
	argv []string
}

func fakeT3ProbeEnv(env map[string]string, procs map[int]t3FakeProc, lookups *int) t3Probe {
	return t3Probe{
		getenv: func(k string) string { return env[k] },
		ppid:   100,
		info: func(_ context.Context, pid int) (int, string, bool) {
			if lookups != nil {
				*lookups++
			}
			p, ok := procs[pid]
			return p.ppid, p.comm, ok
		},
		args: func(_ context.Context, pid int) ([]string, bool) {
			if lookups != nil {
				*lookups++
			}
			p, ok := procs[pid]
			return p.argv, ok && p.argv != nil
		},
	}
}

func fakeT3Probe(entrypoint string, procs map[int]t3FakeProc, lookups *int) t3Probe {
	return fakeT3ProbeEnv(map[string]string{"CLAUDE_CODE_ENTRYPOINT": entrypoint}, procs, lookups)
}

const t3DesktopComm = "/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)"

var t3DesktopArgv = []string{
	t3DesktopComm,
	"--require", "/Applications/T3 Code (Alpha).app/Contents/Resources/app.asar/apps/desktop/dist-electron/compileCache.cjs",
	"/Applications/T3 Code (Alpha).app/Contents/Resources/app.asar/apps/server/dist/bin.mjs",
	"--bootstrap-fd", "3",
}

func t3DesktopTree() map[int]t3FakeProc {
	return map[int]t3FakeProc{
		100: {ppid: 99, comm: "/bin/sh"},
		99:  {ppid: 98, comm: "claude"},
		98:  {ppid: 97, comm: t3DesktopComm, argv: t3DesktopArgv},
		97:  {ppid: 1, comm: t3DesktopComm, argv: []string{t3DesktopComm}},
	}
}

func launchedBy(argv ...string) map[int]t3FakeProc {
	return map[int]t3FakeProc{
		100: {ppid: 99, comm: "sh"},
		99:  {ppid: 98, comm: "claude"},
		98:  {ppid: 1, comm: filepath.Base(argv[0]), argv: argv},
	}
}

func TestSpawnedByT3(t *testing.T) {
	cases := []struct {
		name       string
		entrypoint string
		procs      map[int]t3FakeProc
		want       bool
	}{
		{"desktop app server", "sdk-ts", t3DesktopTree(), true},
		{"linux desktop server", "sdk-ts", launchedBy("/opt/T3 Code/t3code", "/opt/T3 Code/resources/app.asar/apps/server/dist/bin.mjs", "--bootstrap-fd", "3"), true},
		{"npm 0.1 platform binary", "sdk-ts", launchedBy("/home/u/.npm-global/lib/node_modules/t3/node_modules/@t3code/t3-linux-x64/t3", "serve"), true},
		{"npm 0.1 darwin binary", "sdk-ts", launchedBy("/opt/homebrew/lib/node_modules/@t3code/t3-darwin-arm64/t3", "serve"), true},
		{"boot service runtime", "sdk-ts", launchedBy("/home/u/.t3/runtime/versions/0.1.0/t3", "serve"), true},
		{"runtime under spaced path", "sdk-ts", launchedBy("/Users/u/My  Stuff/.t3/runtime/versions/0.1.0/t3", "serve"), true},
		{"npm 0.0 package entry", "sdk-ts", launchedBy("node", "/home/u/.npm/_npx/abc/node_modules/t3/dist/bin.mjs", "serve"), true},
		{"npm 0.0 bin link", "sdk-ts", launchedBy("node", "/opt/homebrew/bin/t3", "serve"), true},
		{"node flag before t3 script", "sdk-ts", launchedBy("/usr/bin/node", "--max-old-space-size=4096", "/usr/local/bin/t3", "serve"), true},
		{"claude wrapper script", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 98, comm: "claude"},
			98:  {ppid: 97, comm: "bash"},
			97:  {ppid: 1, comm: t3DesktopComm, argv: t3DesktopArgv},
		}, true},
		{"interactive claude", "cli", t3DesktopTree(), false},
		{"no entrypoint", "", t3DesktopTree(), false},
		{"other sdk app", "sdk-ts", launchedBy("node", "/Users/u/agent/run.mjs"), false},
		{"unrelated apps/server bin.mjs", "sdk-ts", launchedBy("node", "/work/other/apps/server/dist/bin.mjs"), false},
		{"unrelated asar server without bootstrap fd", "sdk-ts", launchedBy("/Applications/Other.app/Contents/MacOS/Other", "/Applications/Other.app/Contents/Resources/app.asar/apps/server/dist/bin.mjs"), false},
		{"bin.mjs suffix", "sdk-ts", launchedBy("node", "/tmp/node_modules/t3/dist/bin.mjs.backup"), false},
		{"t3 path as option value", "sdk-ts", launchedBy("node", "agent.mjs", "--fixture=/tmp/node_modules/t3/dist/bin.mjs"), false},
		{"t3 path as later argument", "sdk-ts", launchedBy("node", "agent.mjs", "/usr/local/bin/t3"), false},
		{"t3 as an argument of another program", "sdk-ts", launchedBy("/usr/bin/python3", "t3"), false},
		{"relative checkout entry", "sdk-ts", launchedBy("node", "dist/bin.mjs"), false},
		{"sdk script in a T3 terminal", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 98, comm: "claude"},
			98:  {ppid: 97, comm: "node", argv: []string{"node", "agent.mjs"}},
			97:  {ppid: 96, comm: "-zsh", argv: []string{"-zsh"}},
			96:  {ppid: 1, comm: t3DesktopComm, argv: t3DesktopArgv},
		}, false},
		{"t3-named lookalike", "sdk-ts", launchedBy("/opt/t3-tools/t3x", "serve"), false},
		{"orphaned claude", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 1, comm: "claude"},
		}, false},
		{"launcher gone before argv read", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 98, comm: "claude"},
			98:  {ppid: 1, comm: t3DesktopComm},
		}, false},
		{"lookup fails", "sdk-ts", map[int]t3FakeProc{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fakeT3Probe(c.entrypoint, c.procs, nil).spawnedByT3(context.Background()); got != c.want {
				t.Errorf("spawnedByT3() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSpawnedByT3_SDKVersionGate(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli", "CLAUDE_AGENT_SDK_VERSION": "0.3.276"}
	if !fakeT3ProbeEnv(env, t3DesktopTree(), nil).spawnedByT3(context.Background()) {
		t.Error("an inherited cli entrypoint must not hide an SDK child that T3 launched")
	}
}

func TestSpawnedByT3_InteractiveSessionsSkipProcessLookups(t *testing.T) {
	lookups := 0
	fakeT3Probe("cli", t3DesktopTree(), &lookups).spawnedByT3(context.Background())
	if lookups != 0 {
		t.Errorf("lookups = %d; a non-SDK session must not pay for the ancestry walk", lookups)
	}
}

func TestSpawnedByT3_LookupsPerProbe(t *testing.T) {
	lookups := 0
	fakeT3Probe("sdk-ts", t3DesktopTree(), &lookups).spawnedByT3(context.Background())
	if lookups != 4 {
		t.Errorf("lookups = %d, want 4 (sh, claude, launcher, launcher argv)", lookups)
	}
}

func TestSpawnedByT3_StalledLookupHitsBudget(t *testing.T) {
	p := fakeT3Probe("sdk-ts", nil, nil)
	p.info = func(ctx context.Context, _ int) (int, string, bool) {
		select {
		case <-ctx.Done():
			return 0, "", false
		case <-time.After(10 * time.Second):
			return 0, "", false
		}
	}
	start := time.Now()
	if p.spawnedByT3(context.Background()) {
		t.Fatal("a stalled lookup must report the session")
	}
	if d := time.Since(start); d > t3ProbeBudget+time.Second {
		t.Errorf("probe took %v; the lookup context must carry the %v budget", d, t3ProbeBudget)
	}
}

func TestPSOutput_StalledSubprocessIsCancelled(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	orig := psBin
	psBin = script
	t.Cleanup(func() { psBin = orig })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := psOutput(ctx, "-o", "ppid=", "-p", "1"); err == nil {
		t.Fatal("psOutput returned no error for a stalled ps")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("psOutput took %v after its context expired", d)
	}
}

func TestParseProcArgs2(t *testing.T) {
	buf := binary.LittleEndian.AppendUint32(nil, 3)
	buf = append(buf, "/Users/u/My  Stuff/t3\x00\x00\x00\x00"...)
	buf = append(buf, "/Users/u/My  Stuff/t3\x00serve\x00--port=1\x00HOME=/Users/u\x00SECRET=x\x00"...)
	got, ok := parseProcArgs2(buf)
	want := []string{"/Users/u/My  Stuff/t3", "serve", "--port=1"}
	if !ok || len(got) != len(want) {
		t.Fatalf("parseProcArgs2 = %q, %v; want %q", got, ok, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, bad := range [][]byte{nil, {1, 0}, binary.LittleEndian.AppendUint32(nil, 5)} {
		if _, ok := parseProcArgs2(bad); ok {
			t.Errorf("parseProcArgs2(%v) ok on a truncated buffer", bad)
		}
	}
}

func TestParseProcArgs2_HugeArgcIsBoundedByTheBuffer(t *testing.T) {
	rest := []byte("/bin/t3\x00serve\x00")
	if got := argvCap(math.MaxInt32, rest); got != len(rest) {
		t.Errorf("argvCap(MaxInt32, %d bytes) = %d, want %d", len(rest), got, len(rest))
	}
	if got := argvCap(2, rest); got != 2 {
		t.Errorf("argvCap(2, %d bytes) = %d, want 2", len(rest), got)
	}
	buf := binary.LittleEndian.AppendUint32(nil, math.MaxUint32)
	buf = append(buf, "/bin/t3\x00\x00/bin/t3\x00serve\x00"...)
	if _, ok := parseProcArgs2(buf); ok {
		t.Error("parseProcArgs2 ok with argc past the buffer")
	}
}

func TestSpawnedByT3_InheritedSDKEnvHidesAT3TerminalClaude(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli", "CLAUDE_AGENT_SDK_VERSION": "0.3.276"}
	procs := map[int]t3FakeProc{
		100: {ppid: 99, comm: "sh"},
		99:  {ppid: 98, comm: "claude"},
		98:  {ppid: 96, comm: "-zsh", argv: []string{"-zsh"}},
		96:  {ppid: 1, comm: t3DesktopComm, argv: t3DesktopArgv},
	}
	if !fakeT3ProbeEnv(env, procs, nil).spawnedByT3(context.Background()) {
		t.Error("known limit changed: update the T3 terminal note in ARCHITECTURE.md and RUNBOOK.md")
	}
}

func useT3Probe(t *testing.T, p t3Probe) {
	t.Helper()
	orig := newT3Probe
	newT3Probe = func() t3Probe { return p }
	t.Cleanup(func() { newT3Probe = orig })
}

func TestHook_T3SpawnedSessionPostsNothing(t *testing.T) {
	events := []struct{ event, body string }{
		{"session-start", `{"session_id":"t3s","cwd":"/repo","source":"startup"}`},
		{"user-prompt-submit", `{"session_id":"t3s","cwd":"/repo","prompt":"go"}`},
		{"pre-tool-use", `{"session_id":"t3s","cwd":"/repo","tool_name":"Bash","tool_use_id":"u1","tool_input":{"command":"ls"}}`},
		{"permission-request", `{"session_id":"t3s","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"ls"}}`},
		{"post-tool-use", `{"session_id":"t3s","cwd":"/repo","tool_name":"Bash","tool_use_id":"u1","tool_input":{"command":"ls"}}`},
		{"post-tool-use-failure", `{"session_id":"t3s","cwd":"/repo","tool_name":"Bash","tool_use_id":"u1","tool_input":{"command":"ls"},"error":"Exit code 1"}`},
		{"permission-denied", `{"session_id":"t3s","cwd":"/repo","tool_name":"Bash","tool_use_id":"u1","tool_input":{"command":"ls"}}`},
		{"notification", `{"session_id":"t3s","cwd":"/repo","notification_type":"permission_prompt","message":"approve"}`},
		{"stop", `{"session_id":"t3s","cwd":"/repo","last_assistant_message":"done"}`},
		{"stop-failure", `{"session_id":"t3s","cwd":"/repo","error":"rate_limit"}`},
		{"session-end", `{"session_id":"t3s","cwd":"/repo","reason":"other"}`},
	}
	h := newHookHarness(t)
	useT3Probe(t, fakeT3Probe("sdk-ts", t3DesktopTree(), nil))
	owner := detectOwner
	detectOwner = func() (int, string) { t.Fatal("owner lookup ran for a T3 session"); return 0, "" }
	t.Cleanup(func() { detectOwner = owner })
	for _, e := range events {
		dispatchHookForTest(t, e.event, json.RawMessage(e.body))
	}
	if n := h.posts.Load() + h.deletes.Load(); n != 0 {
		t.Errorf("requests = %d (%v), want none for a T3-spawned session", n, *h.bodies)
	}
	if _, err := os.Stat(h.sessionsDir()); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(h.sessionsDir())
		t.Errorf("state dir touched (err=%v, %d entries); the heartbeat daemon must find no marker", err, len(entries))
	}
}

func TestHook_T3SessionReapsMarkerFromEarlierMiss(t *testing.T) {
	h := newHookHarness(t)
	owner := detectOwner
	detectOwner = func() (int, string) { return 0, "" }
	t.Cleanup(func() { detectOwner = owner })
	failing := t3DesktopTree()
	delete(failing, 98)
	useT3Probe(t, fakeT3Probe("sdk-ts", failing, nil))
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"t3s","cwd":"/repo","prompt":"go"}`))
	marker := markerPath(h.sessionsDir(), "t3s")
	if h.posts.Load() != 1 {
		t.Fatalf("posts = %d; a failed lookup must fail open and report", h.posts.Load())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing after the failed lookup: %v", err)
	}
	useT3Probe(t, fakeT3Probe("sdk-ts", t3DesktopTree(), nil))
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"t3s","cwd":"/repo","tool_name":"Bash","tool_use_id":"u1","tool_input":{"command":"ls"}}`))
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker still there (err=%v); the heartbeat would keep re-posting it", err)
	}
	if h.posts.Load() != 1 || h.deletes.Load() != 1 {
		t.Errorf("posts=%d deletes=%d, want 1 and 1: the earlier claude row must be deleted", h.posts.Load(), h.deletes.Load())
	}
	dispatchHookForTest(t, "stop", []byte(`{"session_id":"t3s","cwd":"/repo","last_assistant_message":"ok"}`))
	if h.posts.Load() != 1 || h.deletes.Load() != 1 {
		t.Errorf("posts=%d deletes=%d after the reap, want no more requests", h.posts.Load(), h.deletes.Load())
	}
}

func TestHook_NonT3SDKSessionStillPosts(t *testing.T) {
	h := newHookHarness(t)
	useT3Probe(t, fakeT3Probe("sdk-ts", launchedBy("node", "/Users/u/agent/run.mjs"), nil))
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"sdk1","cwd":"/repo","prompt":"go"}`))
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1 for an SDK session T3 did not spawn", h.posts.Load())
	}
	if _, err := os.Stat(markerPath(h.sessionsDir(), "sdk1")); err != nil {
		t.Errorf("marker missing: %v", err)
	}
}

func TestProcArgv_ReadsOwnArgv(t *testing.T) {
	got, ok := procArgv(context.Background(), os.Getpid())
	if !ok || len(got) != len(os.Args) {
		t.Fatalf("procArgv(self) = %q, %v; want %q", got, ok, os.Args)
	}
	for i := range os.Args {
		if got[i] != os.Args[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], os.Args[i])
		}
	}
}
