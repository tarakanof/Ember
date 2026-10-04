package main

import (
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

func rotateProducerLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, name := range []string{"ember-claude-producer", "ember-tick"} {
		producer.RotateLogIfLarge(producer.LogPath(home, name), producer.DefaultLogThreshold)
	}
}
