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
	// unix s.
	StateChangedAt int64 `json:"state_changed_at,omitempty"`
	// unix ms; unchanged refreshes skip the rewrite, so the file mtime does not track it.
	StatuslineChangedMs int64 `json:"statusline_changed_ms,omitempty"`
	// unix s.
	StatuslineAt int64 `json:"statusline_at,omitempty"`
	// unix s.
	HookAt int64 `json:"hook_at,omitempty"`
	ToolTrack
}

func (m marker) usageSeenAt(mtime time.Time) time.Time {
	if m.StatuslineChangedMs != 0 {
		return time.UnixMilli(m.StatuslineChangedMs)
	}
	return mtime
}

type ToolTrack struct {
	PendingPermission string `json:"pending_permission,omitempty"`
	PendingToolUseID  string `json:"pending_tool_use_id,omitempty"`
	LastToolUseID     string `json:"last_tool_use_id,omitempty"`
	LastToolFP        string `json:"last_tool_fp,omitempty"`
	ResumedTool       string `json:"resumed_tool,omitempty"`
	// unix s.
	ResumedAt      int64 `json:"resumed_at,omitempty"`
	BackgroundWake bool  `json:"bg_wake,omitempty"`
	AgentsWait     bool  `json:"agents_wait,omitempty"`
	AgentsRun      bool  `json:"agents_run,omitempty"`
}

var shellComms = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
	"ksh": true, "csh": true, "tcsh": true, "login": true, "env": true,
}

// Linux /proc and ps cut the task name to 15 bytes.
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

// Without a readable start time the owner is dead only if the pid is gone: a failed lookup must not reap a live session.
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
