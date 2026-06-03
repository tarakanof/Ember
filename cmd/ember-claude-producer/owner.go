package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

// marker is the on-disk session record: the wire StatusRequest plus local-only
// owner-liveness fields. owner_pid/owner_start identify the Claude Code process
// that owns this session, letting the heartbeat drop the session as soon as
// that process exits (window closed, crash — no SessionEnd fires) instead of
// keeping it alive via re-POST until the marker TTL. These fields never reach
// the server: the tick POSTs the embedded StatusRequest, which omits them.
type marker struct {
	producer.StatusRequest
	OwnerPID   int    `json:"owner_pid,omitempty"`
	OwnerStart string `json:"owner_start,omitempty"`
}

var shellComms = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
	"ksh": true, "csh": true, "tcsh": true, "login": true, "env": true,
}

// detectOwner walks up from this hook process to the owning Claude Code
// process, returning its PID and start time. A hook runs as
// claude -> sh -> ember-claude-producer, so the first non-shell, non-self
// ancestor is Claude. Returns (0, "") when it can't be determined.
// Overridable in tests.
var detectOwner = func() (int, string) {
	pid := resolveOwner(os.Getppid(), procParentComm)
	if pid <= 0 {
		return 0, ""
	}
	start, _ := procStart(pid)
	return pid, start
}

// resolveOwner is the testable core of detectOwner: walk ancestors, skipping
// shell wrappers and our own binary, and return the first real ancestor.
func resolveOwner(startPID int, info func(int) (ppid int, comm string, ok bool)) int {
	cur := startPID
	for i := 0; i < 12 && cur > 1; i++ {
		ppid, comm, ok := info(cur)
		if !ok {
			return 0
		}
		base := filepath.Base(strings.TrimPrefix(comm, "-"))
		if !shellComms[base] && base != "ember-claude-producer" {
			return cur
		}
		cur = ppid
	}
	return 0
}

func procParentComm(pid int) (int, string, bool) {
	out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", false
	}
	return ppid, strings.Join(fields[1:], " "), true
}

// procStart returns the process start time (ps lstart — a stable absolute
// timestamp used to guard against PID reuse) and whether the process exists.
func procStart(pid int) (string, bool) {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

// ownerAlive reports whether the recorded owner process is still running as the
// same process (PID present and start time unchanged, so a reused PID reads as
// dead). A missing recorded start time falls back to a bare existence check.
// Overridable in tests.
var ownerAlive = func(pid int, start string) bool {
	cur, ok := procStart(pid)
	if !ok {
		return false
	}
	if start == "" {
		return true
	}
	return cur == start
}

// markerOwner reads the owner identity recorded in a marker file. ok is false
// when the marker has no recorded owner (pre-upgrade markers, or detection
// failed) — such markers fall back to TTL-based cleanup.
func markerOwner(markerP string) (pid int, start string, ok bool) {
	body, err := readMarker(markerP)
	if err != nil {
		return 0, "", false
	}
	var m marker
	if err := json.Unmarshal(body, &m); err != nil {
		return 0, "", false
	}
	if m.OwnerPID <= 0 {
		return 0, "", false
	}
	return m.OwnerPID, m.OwnerStart, true
}
