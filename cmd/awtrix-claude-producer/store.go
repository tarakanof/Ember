package main

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
)

const maxSessionIDLen = 64

var sessionIDAllowed = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "awtrix-ai-status", "sessions"), nil
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
