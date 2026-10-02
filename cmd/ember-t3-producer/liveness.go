package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// serverAlive reports whether the T3 server that owns home is running. T3
// writes <home>/userdata/server-runtime.json at startup and removes it on a
// clean exit; a crash leaves it behind, so the recorded pid is checked too,
// along with its start time where the OS reports it (the pid may have been
// reused after a reboot).
// Without this, a thread caught "running" when T3 quit would stay on the
// clock forever, because the database is never updated again.
func serverAlive(home string, alive func(pid int, startedAt time.Time) bool) bool {
	body, err := os.ReadFile(filepath.Join(home, "userdata", "server-runtime.json"))
	if err != nil {
		return false
	}
	var st struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"startedAt"`
	}
	if json.Unmarshal(body, &st) != nil || st.PID <= 0 {
		return false
	}
	started, _ := time.Parse(time.RFC3339Nano, st.StartedAt)
	return alive(st.PID, started)
}

// pidAlive reports whether pid exists and, when startedAt is known and the OS
// reports process start times, started no later than startedAt (+1 s slack:
// T3 records startedAt after its process is already running).
func pidAlive(pid int, startedAt time.Time) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if startedAt.IsZero() {
		return true
	}
	start, ok := processStart(pid)
	if !ok {
		return true
	}
	return !start.After(startedAt.Add(time.Second))
}
