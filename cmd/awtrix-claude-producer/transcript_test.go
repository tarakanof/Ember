package main

import (
	"errors"
	"os"
	"path/filepath"
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
