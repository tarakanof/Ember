package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spikeLogTestPath mirrors spikeLogPath for test assertions.
func spikeLogTestPath(home string) string {
	return filepath.Join(home, ".local", "state", "ember", "spike-hooks.jsonl")
}

// TestHook_PostToolUse_WritesSpikeLogOnly is the #76 spike regression:
// PostToolUse must append a structured line to the spike log and must NOT
// touch the state machine (no POST/DELETE, no marker write) — this ticket is
// log-only evaluation, not a state-machine change.
func TestHook_PostToolUse_WritesSpikeLogOnly(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "PostToolUse", SessionID: "abc", CWD: "/repo", ToolName: "Bash"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "post-tool-use", body)

	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("post-tool-use must not touch the state machine: posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
	if _, err := os.Stat(filepath.Join(h.sessionsDir(), "abc.json")); !os.IsNotExist(err) {
		t.Errorf("post-tool-use must not write a marker")
	}
	raw, err := os.ReadFile(spikeLogTestPath(h.home))
	if err != nil {
		t.Fatalf("spike log not written: %v", err)
	}
	if !strings.Contains(string(raw), `"event":"post-tool-use"`) {
		t.Errorf("spike log missing event field: %s", raw)
	}
	if !strings.Contains(string(raw), `"tool_name":"Bash"`) {
		t.Errorf("spike log missing tool_name field: %s", raw)
	}
	if !strings.Contains(string(raw), `"session":"abc"`) {
		t.Errorf("spike log missing session field: %s", raw)
	}
	if !strings.Contains(string(raw), `"timestamp":"`) {
		t.Errorf("spike log missing timestamp field: %s", raw)
	}
}

// TestHook_PostToolUseFailure_WritesSpikeLogWithError verifies error-ish
// payload fields are captured on the failure variant.
func TestHook_PostToolUseFailure_WritesSpikeLogWithError(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "PostToolUseFailure", SessionID: "abc", CWD: "/repo",
		ToolName: "Bash", ErrorType: "exit_code", Error: "exit status 1"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "post-tool-use-failure", body)

	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("post-tool-use-failure must not touch the state machine: posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
	raw, err := os.ReadFile(spikeLogTestPath(h.home))
	if err != nil {
		t.Fatalf("spike log not written: %v", err)
	}
	if !strings.Contains(string(raw), `"error_type":"exit_code"`) {
		t.Errorf("spike log missing error_type: %s", raw)
	}
	if !strings.Contains(string(raw), `"error":"exit status 1"`) {
		t.Errorf("spike log missing error: %s", raw)
	}
}

// TestHook_PermissionDenied_WritesSpikeLogOnly is the third #76 event.
func TestHook_PermissionDenied_WritesSpikeLogOnly(t *testing.T) {
	h := newHookHarness(t)
	in := hookInput{HookEventName: "PermissionDenied", SessionID: "abc", CWD: "/repo", ToolName: "Bash"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "permission-denied", body)

	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("permission-denied must not touch the state machine: posts=%d deletes=%d", h.posts.Load(), h.deletes.Load())
	}
	if _, err := os.Stat(filepath.Join(h.sessionsDir(), "abc.json")); !os.IsNotExist(err) {
		t.Errorf("permission-denied must not write a marker")
	}
	raw, err := os.ReadFile(spikeLogTestPath(h.home))
	if err != nil {
		t.Fatalf("spike log not written: %v", err)
	}
	if !strings.Contains(string(raw), `"event":"permission-denied"`) {
		t.Errorf("spike log missing event field: %s", raw)
	}
}

// TestWriteSpikeLog_SkipsWhenFileExceedsCap is the #76 size-cap regression:
// once the spike log exceeds 5 MB, further writes are skipped (best-effort,
// never fatal, never rotated — this is a throwaway evaluation log).
func TestWriteSpikeLog_SkipsWhenFileExceedsCap(t *testing.T) {
	h := newHookHarness(t)
	path := spikeLogTestPath(h.home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, spikeLogMaxBytes+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookInput{HookEventName: "PostToolUse", SessionID: "abc", CWD: "/repo", ToolName: "Bash"}
	body, _ := json.Marshal(in)
	dispatchHookForTest(t, "post-tool-use", body)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(big)) {
		t.Errorf("spike log should not grow past cap: size = %d, want %d", info.Size(), len(big))
	}
}
