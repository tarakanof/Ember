package main

import (
	"os"
	"path/filepath"

	"github.com/tarakanof/ember/internal/producer"
)

func markerPath(stateDir, uuid string) string {
	return filepath.Join(stateDir, uuid+".json")
}

func writeMarker(stateDir, uuid string, body []byte) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return producer.WriteFileAtomic(markerPath(stateDir, uuid), body, 0o600)
}

func removeMarker(stateDir, uuid string) error {
	if err := os.Remove(markerPath(stateDir, uuid)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
