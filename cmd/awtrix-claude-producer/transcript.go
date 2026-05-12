package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// findTranscriptPath returns the absolute path of the JSONL transcript
// file for sessionID, looking under ~/.claude/projects/*/sessionID.jsonl.
// Returns a wrapped os.ErrNotExist when no match is found.
func findTranscriptPath(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	pattern := filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("transcript for session %s: %w", sessionID, os.ErrNotExist)
	}
	return matches[0], nil
}
