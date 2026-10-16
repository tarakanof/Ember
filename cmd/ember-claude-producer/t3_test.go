package main

import (
	"encoding/json"
	"os"
	"testing"
)

type t3FakeProc struct {
	ppid int
	comm string
	args string
}

func fakeT3Probe(entrypoint string, procs map[int]t3FakeProc, lookups *int) t3Probe {
	return t3Probe{
		getenv: func(k string) string {
			if k == "CLAUDE_CODE_ENTRYPOINT" {
				return entrypoint
			}
			return ""
		},
		ppid: 100,
		info: func(pid int) (int, string, bool) {
			if lookups != nil {
				*lookups++
			}
			p, ok := procs[pid]
			return p.ppid, p.comm, ok
		},
		args: func(pid int) (string, bool) {
			if lookups != nil {
				*lookups++
			}
			p, ok := procs[pid]
			return p.args, ok
		},
	}
}

const (
	t3DesktopArgs = "/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha) --require /Applications/T3 Code (Alpha).app/Contents/Resources/app.asar/apps/desktop/dist-electron/compileCache.cjs /Applications/T3 Code (Alpha).app/Contents/Resources/app.asar/apps/server/dist/bin.mjs --bootstrap-fd 3"
	t3DesktopComm = "/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)"
)

func t3DesktopTree() map[int]t3FakeProc {
	return map[int]t3FakeProc{
		100: {ppid: 99, comm: "/bin/sh"},
		99:  {ppid: 98, comm: "claude"},
		98:  {ppid: 97, comm: t3DesktopComm, args: t3DesktopArgs},
		97:  {ppid: 1, comm: t3DesktopComm, args: "/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)"},
	}
}

func TestSpawnedByT3(t *testing.T) {
	launchedBy := func(args string) map[int]t3FakeProc {
		return map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 98, comm: "claude"},
			98:  {ppid: 1, comm: "node", args: args},
		}
	}
	cases := []struct {
		name       string
		entrypoint string
		procs      map[int]t3FakeProc
		want       bool
	}{
		{"desktop app server", "sdk-ts", t3DesktopTree(), true},
		{"linux desktop server", "sdk-ts", launchedBy("/opt/T3 Code/t3code /opt/T3 Code/resources/app.asar/apps/server/dist/bin.mjs --bootstrap-fd 3"), true},
		{"npm package entry", "sdk-ts", launchedBy("node /home/u/.npm/_npx/abc/node_modules/t3/dist/bin.mjs serve"), true},
		{"npm bin link", "sdk-ts", launchedBy("node /opt/homebrew/bin/t3 serve"), true},
		{"standalone t3 binary", "sdk-ts", launchedBy("/usr/local/bin/t3 serve"), true},
		{"claude wrapper script", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 98, comm: "claude"},
			98:  {ppid: 97, comm: "bash"},
			97:  {ppid: 1, comm: t3DesktopComm, args: t3DesktopArgs},
		}, true},
		{"interactive claude", "cli", t3DesktopTree(), false},
		{"no entrypoint", "", t3DesktopTree(), false},
		{"other sdk app", "sdk-ts", launchedBy("node /Users/u/agent/run.mjs"), false},
		{"sdk script in a T3 terminal", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 98, comm: "claude"},
			98:  {ppid: 97, comm: "node", args: "node agent.mjs"},
			97:  {ppid: 96, comm: "-zsh", args: "-zsh"},
			96:  {ppid: 1, comm: t3DesktopComm, args: t3DesktopArgs},
		}, false},
		{"t3-named lookalike", "sdk-ts", launchedBy("node /opt/t3-tools/t3x serve"), false},
		{"orphaned claude", "sdk-ts", map[int]t3FakeProc{
			100: {ppid: 99, comm: "sh"},
			99:  {ppid: 1, comm: "claude"},
		}, false},
		{"lookup fails", "sdk-ts", map[int]t3FakeProc{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fakeT3Probe(c.entrypoint, c.procs, nil).spawnedByT3(); got != c.want {
				t.Errorf("spawnedByT3() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSpawnedByT3_InteractiveSessionsSkipProcessLookups(t *testing.T) {
	lookups := 0
	fakeT3Probe("cli", t3DesktopTree(), &lookups).spawnedByT3()
	if lookups != 0 {
		t.Errorf("lookups = %d; a non-SDK session must not pay for the ancestry walk", lookups)
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

func TestHook_NonT3SDKSessionStillPosts(t *testing.T) {
	h := newHookHarness(t)
	useT3Probe(t, fakeT3Probe("sdk-ts", map[int]t3FakeProc{
		100: {ppid: 99, comm: "sh"},
		99:  {ppid: 98, comm: "claude"},
		98:  {ppid: 1, comm: "node", args: "node /Users/u/agent/run.mjs"},
	}, nil))
	dispatchHookForTest(t, "user-prompt-submit", []byte(`{"session_id":"sdk1","cwd":"/repo","prompt":"go"}`))
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1 for an SDK session T3 did not spawn", h.posts.Load())
	}
	if _, err := os.Stat(markerPath(h.sessionsDir(), "sdk1")); err != nil {
		t.Errorf("marker missing: %v", err)
	}
}
