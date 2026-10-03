package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// markerPath names a thread's marker in the shared sessions dir. The "t3-"
// prefix keeps T3 thread ids from colliding with Claude/Codex session ids;
// an id with characters unsafe in a file name is hashed instead.
func markerPath(stateDir, id string) string {
	name := id
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			sum := sha256.Sum256([]byte(id))
			name = hex.EncodeToString(sum[:16])
			break
		}
	}
	if name == "" || name == "." || name == ".." {
		sum := sha256.Sum256([]byte(id))
		name = hex.EncodeToString(sum[:16])
	}
	return filepath.Join(stateDir, "t3-"+name+".json")
}

func writeMarker(stateDir, id string, body []byte) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, markerPath(stateDir, id)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func removeMarker(stateDir, id string) error {
	if err := os.Remove(markerPath(stateDir, id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sweepMarkers removes every T3 marker, so a crash or SIGKILL of the previous
// daemon does not leave stale threads in the menu app.
func sweepMarkers(stateDir string) {
	matches, _ := filepath.Glob(filepath.Join(stateDir, "t3-*.json"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
