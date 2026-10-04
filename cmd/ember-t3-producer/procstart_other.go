//go:build !darwin && !linux

package main

import "time"

// processStart is unknown off darwin; pidAlive falls back to kill(pid, 0).
func processStart(int) (time.Time, bool) { return time.Time{}, false }
