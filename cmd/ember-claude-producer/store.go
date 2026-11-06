package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
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

func writeMarker(markerPath string, body []byte) error {
	return producer.WriteFileAtomic(markerPath, body, 0o600)
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
	return withLockWait(lockPath, op, -1, fn)
}

var errLockBusy = errors.New("session lock busy")

func withLockExWait(lockPath string, wait time.Duration, fn func() error) error {
	return withLockWait(lockPath, syscall.LOCK_EX, wait, fn)
}

func withLockShWait(lockPath string, wait time.Duration, fn func() error) error {
	return withLockWait(lockPath, syscall.LOCK_SH, wait, fn)
}

func withLockWait(lockPath string, op int, wait time.Duration, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := flockWait(int(f.Fd()), op, wait); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func flockWait(fd, op int, wait time.Duration) error {
	if wait < 0 {
		return syscall.Flock(fd, op)
	}
	deadline := time.Now().Add(wait)
	backoff := 2 * time.Millisecond
	for {
		err := syscall.Flock(fd, op|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return errLockBusy
		}
		time.Sleep(min(backoff, left))
		backoff = min(backoff*2, 20*time.Millisecond)
	}
}
