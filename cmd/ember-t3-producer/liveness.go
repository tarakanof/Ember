package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// serverAlive reports whether the T3 server that owns home is running. T3
// writes <home>/userdata/server-runtime.json at startup and removes it on a
// clean exit; a crash leaves it behind, so the recorded pid is checked too.
// Without this, a thread caught "running" when T3 quit would stay on the
// clock forever, because the database is never updated again.
func serverAlive(home string, alive func(pid int) bool) bool {
	body, err := os.ReadFile(filepath.Join(home, "userdata", "server-runtime.json"))
	if err != nil {
		return false
	}
	var st struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(body, &st) != nil || st.PID <= 0 {
		return false
	}
	return alive(st.PID)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
