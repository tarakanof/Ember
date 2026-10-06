package main

import (
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func processStart(pid int) (time.Time, bool) {
	return producer.ProcStartTime(pid)
}
