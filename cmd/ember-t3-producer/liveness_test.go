package main

import (
	"os"
	"path/filepath"
	"testing"
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
	alive := func(pid int) bool { asked = pid; return true }
	if !serverAlive(home, alive) || asked != 4242 {
		t.Fatalf("serverAlive asked pid %d, want 4242", asked)
	}
	if serverAlive(home, func(int) bool { return false }) {
		t.Fatal("dead pid reported alive")
	}
}

func TestServerAliveMissingOrBadFile(t *testing.T) {
	yes := func(int) bool { return true }
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
	if !pidAlive(os.Getpid()) {
		t.Fatal("own pid not alive")
	}
	if pidAlive(-1) || pidAlive(0) {
		t.Fatal("invalid pid alive")
	}
}
