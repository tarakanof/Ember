package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	return <-done
}

func TestRunConfigureThenDeconfigure(t *testing.T) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "XDG_STATE_HOME": "", "EMBER_TOKEN": ""} {
		t.Setenv(k, v)
	}
	out := captureStdout(t, func() { runConfigure(nil) })
	if !strings.Contains(out, "Configure complete.") {
		t.Fatalf("configure output = %q", out)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `" hook stop`) {
		t.Fatalf("settings.json not configured: %s", b)
	}
	if out := captureStdout(t, runDeconfigure); !strings.Contains(out, "Deconfigure complete.") {
		t.Fatalf("deconfigure output = %q", out)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), `" hook `) {
		t.Fatalf("settings.json still has producer hooks: %s", b)
	}
}
