package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindTranscriptPath_ExactMatch(t *testing.T) {
	home := t.TempDir()
	sessionID := "abc-123"
	dir := filepath.Join(home, ".claude", "projects", "-Users-dt-Github-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	got, err := findTranscriptPath(sessionID)
	if err != nil {
		t.Fatalf("findTranscriptPath: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindTranscriptPath_MissingReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, err := findTranscriptPath("missing-session")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist, got %v", err)
	}
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "test-session"
	path := filepath.Join(dir, sessionID+".jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return sessionID
}

func TestComputeContextPct_MissingFile_ReturnsNil(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pct, err := computeContextPct("does-not-exist")
	if pct != nil {
		t.Errorf("pct = %v, want nil", pct)
	}
	if err == nil {
		t.Error("err = nil, want os.ErrNotExist wrapper")
	}
}

func TestComputeContextPct_EmptyFile_ReturnsNil(t *testing.T) {
	sessionID := writeTranscript(t)
	pct, _ := computeContextPct(sessionID)
	if pct != nil {
		t.Errorf("pct = %v, want nil", pct)
	}
}

func TestComputeContextPct_NoAssistantUsage_ReturnsNil(t *testing.T) {
	sessionID := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"last-prompt","sessionId":"x"}`,
	)
	pct, _ := computeContextPct(sessionID)
	if pct != nil {
		t.Errorf("pct = %v, want nil", pct)
	}
}

func TestComputeContextPct_SingleUsage(t *testing.T) {
	// 100k of 200k → 50%
	sessionID := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":1000,"cache_creation_input_tokens":50000,"cache_read_input_tokens":48000,"output_tokens":1000}}}`,
	)
	pct, err := computeContextPct(sessionID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pct == nil {
		t.Fatal("pct is nil, want 50")
	}
	if *pct != 50 {
		t.Errorf("pct = %d, want 50", *pct)
	}
}

func TestComputeContextPct_LatestWins(t *testing.T) {
	sessionID := writeTranscript(t,
		`{"type":"assistant","message":{"usage":{"input_tokens":20000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
		`{"type":"user","message":{"role":"user","content":"more"}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":160000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
	)
	pct, _ := computeContextPct(sessionID)
	if pct == nil || *pct != 80 {
		t.Errorf("pct = %v, want 80", pct)
	}
}

func TestComputeContextPct_Clamps_Over100(t *testing.T) {
	sessionID := writeTranscript(t,
		`{"type":"assistant","message":{"usage":{"input_tokens":300000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
	)
	pct, _ := computeContextPct(sessionID)
	if pct == nil || *pct != 100 {
		t.Errorf("pct = %v, want 100", pct)
	}
}

func TestComputeContextPct_PartialLastLine_Ignored(t *testing.T) {
	sessionID := writeTranscript(t,
		`{"type":"assistant","message":{"usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
	)
	path, _ := findTranscriptPath(sessionID)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"assistant","message":{"usage":{"input_tok`)
	f.Close()
	pct, _ := computeContextPct(sessionID)
	if pct == nil || *pct != 25 {
		t.Errorf("pct = %v, want 25 (partial last line should be ignored)", pct)
	}
}

func TestComputeContextPct_UnknownFields_Tolerated(t *testing.T) {
	sessionID := writeTranscript(t,
		`{"type":"assistant","uuid":"abc","extraNewField":42,"message":{"id":"msg_x","role":"assistant","usage":{"input_tokens":10000,"cache_creation_input_tokens":40000,"cache_read_input_tokens":0,"output_tokens":0,"service_tier":"standard"}}}`,
	)
	pct, _ := computeContextPct(sessionID)
	if pct == nil || *pct != 25 {
		t.Errorf("pct = %v, want 25", pct)
	}
}
