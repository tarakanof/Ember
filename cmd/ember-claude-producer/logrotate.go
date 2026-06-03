package main

import (
	"os"
	"path/filepath"

	"github.com/tarakanof/ember/internal/producer"
)

// rotateProducerLogs rotates the Claude producer's two log files.
func rotateProducerLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, name := range []string{"ember-claude-producer.log", "ember-tick.log"} {
		producer.RotateLogIfLarge(filepath.Join(home, "Library", "Logs", name), producer.DefaultLogThreshold)
	}
}
