package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestWriteAndReadMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	body := []byte(`{"source":"dt-mbp","tool":"claude","session":"x","state":"running"}`)
	if err := writeMarker(path, body); err != nil {
		t.Fatal(err)
	}
	got, err := readMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestWriteMarker_NoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := writeMarker(path, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.json" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

func TestReadMarker_Missing(t *testing.T) {
	_, err := readMarker(filepath.Join(t.TempDir(), "nope.json"))
	if !os.IsNotExist(err) {
		t.Errorf("missing file should yield IsNotExist, got %v", err)
	}
}

func TestWithLockEx_SerializesExclusive(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "x.lock")
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = withLockEx(lock, func() error {
				n := concurrent.Add(1)
				for {
					m := maxConcurrent.Load()
					if n <= m || maxConcurrent.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				concurrent.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()
	if maxConcurrent.Load() != 1 {
		t.Errorf("maxConcurrent = %d, want 1 (LOCK_EX should serialize)", maxConcurrent.Load())
	}
}

func TestWithLockSh_AllowsMultiple(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "x.lock")
	var concurrent atomic.Int32
	var sawConcurrent atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = withLockSh(lock, func() error {
				n := concurrent.Add(1)
				if n >= 2 {
					sawConcurrent.Store(true)
				}
				time.Sleep(20 * time.Millisecond)
				concurrent.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()
	if !sawConcurrent.Load() {
		t.Error("LOCK_SH should allow concurrent holders")
	}
}

func TestWithLockEx_CreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "x.lock")
	if err := withLockEx(lock, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("lock file not created: %v", err)
	}
}
