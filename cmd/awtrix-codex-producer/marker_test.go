package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndRemoveMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions") // not yet created — writeMarker must mkdir
	body := []byte(`{"source":"mbp","tool":"codex","session":"u-1","state":"running"}`)
	if err := writeMarker(dir, "u-1", body); err != nil {
		t.Fatalf("writeMarker: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "u-1.json"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("marker = %q, want %q", got, body)
	}
	if err := removeMarker(dir, "u-1"); err != nil {
		t.Fatalf("removeMarker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "u-1.json")); !os.IsNotExist(err) {
		t.Errorf("marker should be gone, stat err = %v", err)
	}
	if err := removeMarker(dir, "u-1"); err != nil {
		t.Errorf("removeMarker on missing must be nil, got %v", err)
	}
}
