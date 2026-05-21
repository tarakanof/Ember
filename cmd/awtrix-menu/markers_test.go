package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMarker(t *testing.T, dir, id, state, message string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, id+".json")
	body := []byte(`{"source":"x","tool":"claude","session":"` + id + `","state":"` + state + `","message":"` + message + `"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestMarkers_Empty(t *testing.T) {
	v := readView(t.TempDir(), 6*time.Hour)
	if v.DominantState != "" || v.ActiveCount != 0 {
		t.Errorf("empty dir → empty view, got %+v", v)
	}
}

func TestMarkers_DominantPriority(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, "a", "running", "Bash", time.Second)
	writeMarker(t, dir, "b", "waiting", "approve", time.Second)
	writeMarker(t, dir, "c", "running", "Edit", time.Second)
	v := readView(dir, 6*time.Hour)
	if v.DominantState != "waiting" {
		t.Errorf("dominant = %q, want waiting", v.DominantState)
	}
	if v.ActiveCount != 3 {
		t.Errorf("active = %d, want 3", v.ActiveCount)
	}
	if v.LastMessage != "approve" {
		t.Errorf("LastMessage = %q, want approve", v.LastMessage)
	}
}

func TestMarkers_StaleIgnored(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, "old", "running", "Bash", 7*time.Hour)
	writeMarker(t, dir, "fresh", "running", "Edit", time.Second)
	v := readView(dir, 6*time.Hour)
	if v.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1 (stale ignored)", v.ActiveCount)
	}
}

func TestMarkers_DoneNotCounted(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, "a", "done", "Bash", time.Second)
	writeMarker(t, dir, "b", "running", "Edit", time.Second)
	v := readView(dir, 6*time.Hour)
	if v.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1 (done excluded)", v.ActiveCount)
	}
	if v.DominantState != "running" {
		t.Errorf("DominantState = %q, want running", v.DominantState)
	}
}

func TestMarkers_DoneOnlySetIsDominant(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, "a", "done", "Bash", time.Second)
	v := readView(dir, 6*time.Hour)
	if v.DominantState != "done" {
		t.Errorf("DominantState = %q, want done", v.DominantState)
	}
	if v.ActiveCount != 0 {
		t.Errorf("ActiveCount = %d, want 0 (done not active)", v.ActiveCount)
	}
}

func TestMarkers_DeterministicOrdering(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, "z-newer", "running", "Z", time.Second)
	writeMarker(t, dir, "a-older", "running", "A", time.Minute)
	v := readView(dir, 6*time.Hour)
	// Sorted alphabetically by filename; LastMessage from the lexically-first
	if v.LastMessage != "A" {
		t.Errorf("LastMessage = %q, want A (sorted glob, deterministic)", v.LastMessage)
	}
}

func TestReadView_DominantTool(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// waiting (codex) outranks running (claude): waiting > running.
	write("a.json", `{"source":"mbp","tool":"claude","session":"a","state":"running"}`)
	write("b.json", `{"source":"mbp","tool":"codex","session":"b","state":"waiting"}`)

	v := readView(dir, time.Hour)
	if v.DominantState != "waiting" {
		t.Errorf("DominantState = %q, want waiting", v.DominantState)
	}
	if v.DominantTool != "codex" {
		t.Errorf("DominantTool = %q, want codex", v.DominantTool)
	}
}

func TestTTLFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"empty", "", 6 * time.Hour},
		{"explicit_2", "STATUS_HEARTBEAT_TTL_HOURS=2\n", 2 * time.Hour},
		{"fractional", "STATUS_HEARTBEAT_TTL_HOURS=0.5\n", 30 * time.Minute},
		{"unparseable", "STATUS_HEARTBEAT_TTL_HOURS=garbage\n", 6 * time.Hour},
		{"zero_falls_back", "STATUS_HEARTBEAT_TTL_HOURS=0\n", 6 * time.Hour},
		{"negative_falls_back", "STATUS_HEARTBEAT_TTL_HOURS=-1\n", 6 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := parseEnv(strings.NewReader(tc.env))
			if got := ttlFromEnv(rec); got != tc.want {
				t.Errorf("ttlFromEnv(%q) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
