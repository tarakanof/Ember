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

const (
	startProbeGap = 10 * time.Second
	startMaxAge   = time.Minute
)

func pidAlive(pid int, startedAt time.Time) bool {
	return newStartCache().alive(pid, startedAt)
}

type startCache struct {
	lookup     func(pid int) (time.Time, bool)
	now        func() time.Time
	pid        int
	start      time.Time
	known      bool
	lookedUpAt time.Time
	probedAt   time.Time
}

func newStartCache() *startCache {
	return &startCache{lookup: processStart, now: time.Now}
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
	// Wall clock: the monotonic one stops while the Mac sleeps (ARCHITECTURE, T3 producer).
	now := c.now().Round(0)
	gap := now.Sub(c.probedAt)
	c.probedAt = now
	if startedAt.IsZero() {
		return true
	}
	if c.pid != pid || gap > startProbeGap || now.Sub(c.lookedUpAt) >= startMaxAge {
		c.pid, c.lookedUpAt = pid, now
		c.start, c.known = c.lookup(pid)
	}
	if !c.known {
		return true
	}
	return !c.start.After(startedAt.Add(time.Second))
}
