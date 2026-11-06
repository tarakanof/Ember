package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkerPathIsPrefixedAndSafe(t *testing.T) {
	dir := "/state"
	if got := markerPath(dir, "thread-abc_1.2"); got != filepath.Join(dir, "t3-thread-abc_1.2.json") {
		t.Fatalf("markerPath = %q", got)
	}
	got := markerPath(dir, "../../etc/passwd")
	if filepath.Dir(got) != dir {
		t.Fatalf("unsafe id escaped the state dir: %q", got)
	}
}

func TestWriteRemoveAndSweepMarkers(t *testing.T) {
	dir := t.TempDir()
	if err := writeMarker(dir, "a", []byte(`{"tool":"t3"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(dir, "b", []byte(`{"tool":"t3"}`)); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "claude-session.json")
	if err := os.WriteFile(other, []byte(`{"tool":"claude"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeMarker(dir, "a"); err != nil {
		t.Fatal(err)
	}
	if err := removeMarker(dir, "a"); err != nil {
		t.Fatalf("removing a missing marker must not error: %v", err)
	}
	sweepMarkers(dir)
	if _, err := os.Stat(markerPath(dir, "b")); !os.IsNotExist(err) {
		t.Fatal("sweep left a t3 marker behind")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("sweep removed another producer's marker")
	}
}
