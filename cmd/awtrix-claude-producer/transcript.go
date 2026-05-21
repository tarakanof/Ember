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

// defaultContextWindowTokens is the fallback model context window used
// for percent calculation when no override is configured and the model
// is unrecognized. 200_000 covers Sonnet 4.x, Haiku 4.x, and Opus 4.x
// without the 1M-context flag.
const defaultContextWindowTokens = 200_000

// modelContextWindow returns the assumed context window for a model ID
// read from the transcript. The 1M-context beta is NOT expressed in the
// model string (an Opus 1M session still reports "claude-opus-4-7"), so
// callers that run 1M context must set STATUS_CONTEXT_WINDOW_TOKENS to
// override this — model detection alone cannot distinguish the two.
// All currently-known Claude 4.x models share the 200k base window, so
// this returns the default for every recognized and unrecognized model;
// the function exists as the single place to add model-specific windows
// when future models ship with a different base.
func modelContextWindow(model string) int {
	return defaultContextWindowTokens
}

type transcriptUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

type transcriptEntry struct {
	Type    string `json:"type"`
	Message *struct {
		Model string           `json:"model,omitempty"`
		Usage *transcriptUsage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

// computeContextPct scans the session's transcript JSONL for the
// latest assistant entry that carries a non-empty message.usage and
// returns the clamped [0, 100] percentage of the context window it
// represents. The window is overrideWindow when > 0; otherwise it is
// derived from the latest entry's model via modelContextWindow.
// Returns (nil, nil) when the transcript exists but has no usable usage
// data. Returns (nil, err wrapping os.ErrNotExist) when the transcript
// file isn't found.
func computeContextPct(sessionID string, overrideWindow int) (*int, error) {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lastUsage *transcriptUsage
	var lastModel string
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
		lastModel = entry.Message.Model
	}
	if lastUsage == nil {
		return nil, nil
	}
	window := overrideWindow
	if window <= 0 {
		window = modelContextWindow(lastModel)
	}
	total := lastUsage.InputTokens + lastUsage.CacheCreationInputTokens + lastUsage.CacheReadInputTokens + lastUsage.OutputTokens
	pct := total * 100 / window
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, nil
}
