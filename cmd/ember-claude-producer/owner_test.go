package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fakeProc struct {
	ppid int
	comm string
}

func fakeInfo(procs map[int]fakeProc) func(int) (int, string, bool) {
	return func(pid int) (int, string, bool) {
		p, ok := procs[pid]
		if !ok {
			return 0, "", false
		}
		return p.ppid, p.comm, true
	}
}

func TestResolveOwner_SkipsShellToClaude(t *testing.T) {
	procs := map[int]fakeProc{
		90: {ppid: 80, comm: "/bin/sh"},
		80: {ppid: 70, comm: "claude"},
		70: {ppid: 1, comm: "-zsh"},
	}
	if got := resolveOwner(90, fakeInfo(procs)); got != 80 {
		t.Errorf("resolveOwner = %d, want 80 (claude)", got)
	}
}

func TestResolveOwner_NodeIsOwner(t *testing.T) {
	procs := map[int]fakeProc{
		90: {ppid: 80, comm: "/bin/zsh"},
		80: {ppid: 70, comm: "/opt/homebrew/Cellar/node/26.0.0/bin/node"},
		70: {ppid: 1, comm: "-zsh"},
	}
	if got := resolveOwner(90, fakeInfo(procs)); got != 80 {
		t.Errorf("resolveOwner = %d, want 80 (node)", got)
	}
}

func TestResolveOwner_DirectClaude(t *testing.T) {
	procs := map[int]fakeProc{
		80: {ppid: 70, comm: "claude"},
		70: {ppid: 1, comm: "-zsh"},
	}
	if got := resolveOwner(80, fakeInfo(procs)); got != 80 {
		t.Errorf("resolveOwner = %d, want 80 (claude)", got)
	}
}

func TestResolveOwner_SkipsSelf(t *testing.T) {
	procs := map[int]fakeProc{
		100: {ppid: 80, comm: "/Users/joe/go/bin/ember-claude-producer"},
		80:  {ppid: 70, comm: "claude"},
		70:  {ppid: 1, comm: "-zsh"},
	}
	if got := resolveOwner(100, fakeInfo(procs)); got != 80 {
		t.Errorf("resolveOwner = %d, want 80 (claude)", got)
	}
}

func TestResolveOwner_MissingAncestor(t *testing.T) {
	if got := resolveOwner(999, fakeInfo(map[int]fakeProc{})); got != 0 {
		t.Errorf("resolveOwner = %d, want 0 (unknown pid)", got)
	}
}

func TestResolveOwner_NoOwnerAllShells(t *testing.T) {
	procs := map[int]fakeProc{
		90: {ppid: 80, comm: "/bin/sh"},
		80: {ppid: 70, comm: "/bin/zsh"},
		70: {ppid: 1, comm: "login"},
	}
	if got := resolveOwner(90, fakeInfo(procs)); got != 0 {
		t.Errorf("resolveOwner = %d, want 0 (no non-shell ancestor)", got)
	}
}

func TestProcessOneMarker_DeadOwner_Reaped(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "dead.json")
	m := marker{
		StatusRequest: StatusRequest{Source: "test-mbp", Tool: "claude", Session: "dead", State: "running"},
		OwnerPID:      424242,
		OwnerStart:    "Wed May 28 10:00:00 2026",
	}
	body, _ := json.Marshal(m)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}

	restore := ownerAlive
	ownerAlive = func(pid int, start string) bool { return false }
	defer func() { ownerAlive = restore }()

	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)

	if h.deletes.Load() != 1 {
		t.Errorf("dead-owner marker should produce one DELETE; got %d", h.deletes.Load())
	}
	if h.posts.Load() != 0 {
		t.Errorf("dead-owner marker must not be re-POSTed; got %d", h.posts.Load())
	}
	if _, err := os.Stat(markerP); !os.IsNotExist(err) {
		t.Errorf("dead-owner marker should be removed")
	}
}

func TestProcessOneMarker_LiveOwner_NotReaped(t *testing.T) {
	h := newHookHarness(t)
	dir := h.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerP := filepath.Join(dir, "live.json")
	m := marker{
		StatusRequest: StatusRequest{Source: "test-mbp", Tool: "claude", Session: "live", State: "running"},
		OwnerPID:      424242,
		OwnerStart:    "Wed May 28 10:00:00 2026",
	}
	body, _ := json.Marshal(m)
	if err := os.WriteFile(markerP, body, 0o600); err != nil {
		t.Fatal(err)
	}

	restore := ownerAlive
	ownerAlive = func(pid int, start string) bool { return true }
	defer func() { ownerAlive = restore }()

	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)

	if h.posts.Load() != 1 {
		t.Errorf("live-owner marker should be re-POSTed; got %d posts", h.posts.Load())
	}
	if h.deletes.Load() != 0 {
		t.Errorf("live-owner marker must not be deleted; got %d", h.deletes.Load())
	}
	if _, err := os.Stat(markerP); err != nil {
		t.Errorf("live-owner marker should be preserved: %v", err)
	}
}

func TestResolveOwnerSkipsLinuxTruncatedProducerComm(t *testing.T) {
	procs := map[int]struct {
		ppid int
		comm string
	}{
		300: {200, "ember-claude-pr"},
		200: {100, "sh"},
		100: {1, "claude"},
	}
	info := func(pid int) (int, string, bool) {
		p, ok := procs[pid]
		return p.ppid, p.comm, ok
	}
	if got := resolveOwner(300, info); got != 100 {
		t.Fatalf("owner = %d, want 100 (claude)", got)
	}
}

func TestOwnerAliveWith(t *testing.T) {
	startOK := func(s string) func(int) (string, bool) { return func(int) (string, bool) { return s, true } }
	startFail := func(int) (string, bool) { return "", false }
	yes := func(int) bool { return true }
	no := func(int) bool { return false }
	for _, tc := range []struct {
		name   string
		start  string
		of     func(int) (string, bool)
		exists func(int) bool
		want   bool
	}{
		{"same start", "Mon 1", startOK("Mon 1"), no, true},
		{"pid reused", "Mon 1", startOK("Tue 2"), yes, false},
		{"no recorded start", "", startOK("Tue 2"), no, true},
		{"lookup failed, pid live", "Mon 1", startFail, yes, true},
		{"lookup failed, pid gone", "Mon 1", startFail, no, false},
	} {
		if got := ownerAliveWith(42, tc.start, tc.of, tc.exists); got != tc.want {
			t.Errorf("%s: alive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPidExists(t *testing.T) {
	if !pidExists(os.Getpid()) {
		t.Error("own pid reported gone")
	}
	if pidExists(0) || pidExists(-1) {
		t.Error("non-positive pid reported live")
	}
	if pidExists(1 << 22) {
		t.Error("absent pid reported live")
	}
}
