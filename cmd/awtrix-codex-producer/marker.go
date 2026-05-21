package main

import (
	"os"
	"path/filepath"
)

func markerPath(stateDir, uuid string) string {
	return filepath.Join(stateDir, uuid+".json")
}

// writeMarker atomically writes body to <stateDir>/<uuid>.json (temp + rename),
// creating stateDir 0700 first. Markers are not secret, so no chmod.
func writeMarker(stateDir, uuid string, body []byte) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, markerPath(stateDir, uuid)); err != nil {
		cleanup()
		return err
	}
	return nil
}

// removeMarker deletes the marker, tolerating a missing file.
func removeMarker(stateDir, uuid string) error {
	if err := os.Remove(markerPath(stateDir, uuid)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
