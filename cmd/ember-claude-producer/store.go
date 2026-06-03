package main

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

const maxSessionIDLen = 64

var sessionIDAllowed = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ember", "sessions"), nil
}

func sanitizeSessionID(rawID, cwd string) string {
	if rawID != "" && len(rawID) <= maxSessionIDLen && sessionIDAllowed.MatchString(rawID) {
		return rawID
	}
	sum := sha1.Sum([]byte(cwd))
	return hex.EncodeToString(sum[:])[:16]
}

func markerPath(stateDir, sessionID string) string {
	return filepath.Join(stateDir, sessionID+".json")
}

func lockPath(stateDir, sessionID string) string {
	return filepath.Join(stateDir, sessionID+".lock")
}

// writeMarker writes body to markerPath atomically via temp+rename.
// The temp file lives in the same directory so rename is atomic on POSIX.
func writeMarker(markerPath string, body []byte) error {
	dir := filepath.Dir(markerPath)
	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
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
	if err := os.Rename(tmpName, markerPath); err != nil {
		cleanup()
		return err
	}
	return nil
}

func readMarker(markerPath string) ([]byte, error) {
	return os.ReadFile(markerPath)
}

func withLockEx(lockPath string, fn func() error) error {
	return withLock(lockPath, syscall.LOCK_EX, fn)
}

func withLockSh(lockPath string, fn func() error) error {
	return withLock(lockPath, syscall.LOCK_SH, fn)
}

func withLock(lockPath string, op int, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), op); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	// Note: lock file is NEVER deleted (POSIX flock-on-inode constraint).
	return fn()
}
