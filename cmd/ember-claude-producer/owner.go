package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type marker struct {
	producer.StatusRequest
	OwnerPID   int    `json:"owner_pid,omitempty"`
	OwnerStart string `json:"owner_start,omitempty"`
	// StateChangedAt (unix s) is when State last changed; the heartbeat stops
	// re-posting done/error once it is older than DoneTTLSeconds.
	StateChangedAt int64 `json:"state_changed_at,omitempty"`
	// StatuslineAt (unix s) is when the statusline last refreshed the rate
	// and context figures; the usage relay picks the freshest by it. The
	// statusline skips unchanged rewrites, so file mtime does not track it.
	StatuslineAt int64 `json:"statusline_at,omitempty"`
	ToolTrack
}

// usageSeenAt is when the marker's rate figures were last seen: StatuslineAt,
// else (a marker from an older producer) its file mtime.
func (m marker) usageSeenAt(mtime time.Time) time.Time {
	if m.StatuslineAt != 0 {
		return time.Unix(m.StatuslineAt, 0)
	}
	return mtime
}

// ToolTrack is marker-only bookkeeping for the tool-outcome hooks; none of it goes on the wire.
type ToolTrack struct {
	// PendingPermission fingerprints the call a PermissionRequest put the session in waiting for; only that call's outcome ends the wait.
	PendingPermission string `json:"pending_permission,omitempty"`
	PendingToolUseID  string `json:"pending_tool_use_id,omitempty"`
	// LastToolUseID and LastToolFP are the latest PreToolUse's tool_use_id and fingerprint, used to recover the id for a PermissionRequest that has none.
	LastToolUseID string `json:"last_tool_use_id,omitempty"`
	LastToolFP    string `json:"last_tool_fp,omitempty"`
	// ResumedTool and ResumedAt record the last wait an outcome ended, so that dialog's late permission_prompt Notification cannot re-enter waiting.
	ResumedTool string `json:"resumed_tool,omitempty"`
	ResumedAt   int64  `json:"resumed_at,omitempty"`
	// BackgroundWake is set when Stop left the session running for a background subagent, workflow, teammate or cloud session; the agents cross-check then never turns an idle status into done.
	BackgroundWake bool `json:"bg_wake,omitempty"`
	// AgentsWait marks a wait the agents cross-check opened, so it may also close it; any hook write clears it.
	AgentsWait bool `json:"agents_wait,omitempty"`
}

var shellComms = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
	"ksh": true, "csh": true, "tcsh": true, "login": true, "env": true,
}

// producerCommLinux is how Linux /proc and ps show this binary: the task
// name is cut to 15 bytes.
const producerCommLinux = "ember-claude-pr"

var detectOwner = func() (int, string) {
	pid := resolveOwner(os.Getppid(), procParentComm)
	if pid <= 0 {
		return 0, ""
	}
	start, _ := procStart(pid)
	return pid, start
}

func resolveOwner(startPID int, info func(int) (ppid int, comm string, ok bool)) int {
	cur := startPID
	for i := 0; i < 12 && cur > 1; i++ {
		ppid, comm, ok := info(cur)
		if !ok {
			return 0
		}
		base := filepath.Base(strings.TrimPrefix(comm, "-"))
		if !shellComms[base] && base != "ember-claude-producer" && base != producerCommLinux {
			return cur
		}
		cur = ppid
	}
	return 0
}

var ownerAlive = func(pid int, start string) bool {
	return ownerAliveWith(pid, start, procStart, pidExists)
}

// ownerAliveWith reports whether pid is still the process that started at
// start. When the start time cannot be read (ps failed, e.g. a ps without
// lstart), the owner counts as dead only if the pid is gone: a lookup
// failure must not reap a live session.
func ownerAliveWith(pid int, start string, startOf func(int) (string, bool), exists func(int) bool) bool {
	cur, ok := startOf(pid)
	if !ok {
		return exists(pid)
	}
	if start == "" {
		return true
	}
	return cur == start
}

// pidExists probes pid with signal 0; EPERM means it exists under another user.
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

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
