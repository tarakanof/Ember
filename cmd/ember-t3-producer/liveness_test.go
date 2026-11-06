package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeRuntime(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "userdata")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server-runtime.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestServerAliveFromRecordedRuntimeFile(t *testing.T) {
	home := t.TempDir()
	body, err := os.ReadFile(filepath.Join("testdata", "server-runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeRuntime(t, home, string(body))
	var asked int
	var askedStart time.Time
	alive := func(pid int, startedAt time.Time) bool { asked, askedStart = pid, startedAt; return true }
	if !serverAlive(home, alive) || asked != 4242 {
		t.Fatalf("serverAlive asked pid %d, want 4242", asked)
	}
	if want := time.Date(2026, 10, 2, 18, 20, 0, 0, time.UTC); !askedStart.Equal(want) {
		t.Fatalf("startedAt = %v, want %v", askedStart, want)
	}
	if serverAlive(home, func(int, time.Time) bool { return false }) {
		t.Fatal("dead pid reported alive")
	}
}

func TestServerAliveMissingOrBadFile(t *testing.T) {
	yes := func(int, time.Time) bool { return true }
	if serverAlive(t.TempDir(), yes) {
		t.Fatal("missing runtime file reported alive")
	}
	for _, body := range []string{`not json`, `{"version":1,"pid":0}`, `{"version":1}`} {
		home := t.TempDir()
		writeRuntime(t, home, body)
		if serverAlive(home, yes) {
			t.Fatalf("%q reported alive", body)
		}
	}
}

func TestPidAliveSelf(t *testing.T) {
	if !pidAlive(os.Getpid(), time.Now()) {
		t.Fatal("own pid not alive")
	}
	if !pidAlive(os.Getpid(), time.Time{}) {
		t.Fatal("own pid not alive without a recorded start")
	}
	if pidAlive(-1, time.Now()) || pidAlive(0, time.Now()) {
		t.Fatal("invalid pid alive")
	}
}

func TestPidAliveRejectsReusedPid(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("process start time is checked on darwin only")
	}
	if pidAlive(os.Getpid(), time.Now().Add(-24*time.Hour)) {
		t.Fatal("a process that started after the recorded startedAt was accepted")
	}
}
