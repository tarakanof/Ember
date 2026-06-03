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

// The hook process tree is: claude -> sh -> ember-claude-producer. Starting
// from the shell parent, the walk must skip the shell and land on claude.
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

// Some installs run claude as `node`; node is not a shell, so it is returned.
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

// If Claude execs the hook directly (no shell wrapper), the parent is already
// the owner.
func TestResolveOwner_DirectClaude(t *testing.T) {
	procs := map[int]fakeProc{
		80: {ppid: 70, comm: "claude"},
		70: {ppid: 1, comm: "-zsh"},
	}
	if got := resolveOwner(80, fakeInfo(procs)); got != 80 {
		t.Errorf("resolveOwner = %d, want 80 (claude)", got)
	}
}

// Our own binary must be skipped if it appears in the chain.
func TestResolveOwner_SkipsSelf(t *testing.T) {
	procs := map[int]fakeProc{
		100: {ppid: 80, comm: "/Users/dt/go/bin/ember-claude-producer"},
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

// All ancestors are shells up to root: no Claude process found.
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

// A marker whose owning Claude process has exited must be reaped by the
// heartbeat tick immediately (DELETE + marker removed), without waiting out the
// 6h marker TTL.
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

// A marker whose owner is still alive must keep being re-POSTed (idle-but-open
// Claude stays present), not reaped.
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
