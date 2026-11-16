package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

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

func pidAlive(pid int, startedAt time.Time) bool {
	return (&startCache{lookup: processStart}).alive(pid, startedAt)
}

type startCache struct {
	lookup func(pid int) (time.Time, bool)
	pid    int
	start  time.Time
	known  bool
}

// T3 records startedAt after its process is running, hence the 1 s slack.
func (c *startCache) alive(pid int, startedAt time.Time) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		c.pid = 0
		return false
	}
	if startedAt.IsZero() {
		return true
	}
	if c.pid != pid {
		c.pid = pid
		c.start, c.known = c.lookup(pid)
	}
	if !c.known {
		return true
	}
	return !c.start.After(startedAt.Add(time.Second))
}
