package main

import (
	"os"
	"path/filepath"

	"github.com/dt/awtrix-ai-status/internal/producer"
)

// rotateProducerLogs rotates the Claude producer's two log files.
func rotateProducerLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, name := range []string{"awtrix-claude-producer.log", "awtrix-ai-status-tick.log"} {
		producer.RotateLogIfLarge(filepath.Join(home, "Library", "Logs", name), producer.DefaultLogThreshold)
	}
}
