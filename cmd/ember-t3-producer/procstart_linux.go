package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

// processStart is pid's start time from /proc (boot time + start ticks).
func processStart(pid int) (time.Time, bool) {
	return producer.ProcStartTime(pid)
}
