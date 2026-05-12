package main

import (
	"bytes"
	"encoding/json"
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

// contextWindowTokens is the model context window assumed for percent
// calculation. Hard-coded to 200_000 to cover Sonnet 4.x, Haiku 4.x,
// and Opus 4.x (without the 1M flag).
const contextWindowTokens = 200_000

type transcriptUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

type transcriptEntry struct {
	Type    string `json:"type"`
	Message *struct {
		Usage *transcriptUsage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

// computeContextPct scans the session's transcript JSONL for the
// latest assistant entry that carries a non-empty message.usage and
// returns the clamped [0, 100] percentage of contextWindowTokens it
// represents. Returns (nil, nil) when the transcript exists but has
// no usable usage data. Returns (nil, err wrapping os.ErrNotExist)
// when the transcript file isn't found.
func computeContextPct(sessionID string) (*int, error) {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lastUsage *transcriptUsage
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry transcriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" || entry.Message == nil || entry.Message.Usage == nil {
			continue
		}
		lastUsage = entry.Message.Usage
	}
	if lastUsage == nil {
		return nil, nil
	}
	total := lastUsage.InputTokens + lastUsage.CacheCreationInputTokens + lastUsage.CacheReadInputTokens + lastUsage.OutputTokens
	pct := total * 100 / contextWindowTokens
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, nil
}
