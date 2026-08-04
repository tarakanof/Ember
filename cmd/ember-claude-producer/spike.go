package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// spikeLogMaxBytes caps the #76 evaluation log: once the file reaches this
// size, further writes are skipped rather than rotated — this is a throwaway
// log for a few days of real-use data collection, not a durable audit trail.
const spikeLogMaxBytes = 5 * 1024 * 1024

// spikeLogEntry is one line of the #76 evaluation log.
type spikeLogEntry struct {
	Timestamp    string `json:"timestamp"`
	Event        string `json:"event"`
	ToolName     string `json:"tool_name,omitempty"`
	Session      string `json:"session,omitempty"`
	ErrorType    string `json:"error_type,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func spikeLogPath(home string) string {
	return filepath.Join(home, ".local", "state", "ember", "spike-hooks.jsonl")
}

// writeSpikeLog appends a structured line for the #76 spike (see dispatchHook's
// post-tool-use / post-tool-use-failure / permission-denied cases). Best-effort
// and silent on any failure — this is evaluation instrumentation, never allowed
// to affect the hook's exit status or the state machine.
func writeSpikeLog(sessionID, event string, in hookInput) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := spikeLogPath(home)
	if info, err := os.Stat(path); err == nil && info.Size() > spikeLogMaxBytes {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	entry := spikeLogEntry{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Event:        event,
		ToolName:     in.ToolName,
		Session:      sessionID,
		ErrorType:    in.ErrorType,
		Error:        in.Error,
		ErrorMessage: in.ErrorMessage,
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return
	}
	body = append(body, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(body)
}
