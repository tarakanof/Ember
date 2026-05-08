package main

import (
	"strings"
	"testing"
)

func TestSanitizeSessionID_Valid(t *testing.T) {
	got := sanitizeSessionID("abc123_DEF.test-1", "/some/cwd")
	if got != "abc123_DEF.test-1" {
		t.Errorf("got %q, want passthrough", got)
	}
}

func TestSanitizeSessionID_InvalidFallsBackToCwdHash(t *testing.T) {
	got := sanitizeSessionID("not/allowed:chars", "/repo/foo")
	if got == "not/allowed:chars" {
		t.Errorf("invalid characters should not pass through: %q", got)
	}
	if len(got) != 16 {
		t.Errorf("fallback should be 16 hex chars, got %q (len %d)", got, len(got))
	}
	got2 := sanitizeSessionID("", "/repo/foo")
	if got != got2 {
		t.Errorf("fallback must be deterministic across hook invocations: %q vs %q", got, got2)
	}
}

func TestSanitizeSessionID_TooLong(t *testing.T) {
	long := strings.Repeat("a", 65)
	got := sanitizeSessionID(long, "/cwd")
	if got == long {
		t.Errorf(">64 chars should fall back, got passthrough")
	}
	if len(got) != 16 {
		t.Errorf("fallback length = %d, want 16", len(got))
	}
}

func TestMarkerAndLockPaths(t *testing.T) {
	got := markerPath("/state/dir", "abc")
	if got != "/state/dir/abc.json" {
		t.Errorf("markerPath = %q", got)
	}
	got = lockPath("/state/dir", "abc")
	if got != "/state/dir/abc.lock" {
		t.Errorf("lockPath = %q", got)
	}
}
