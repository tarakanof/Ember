package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tarakanof/ember/internal/producer"
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

func entryHome(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "XDG_STATE_HOME": "", "EMBER_TOKEN": "", "CODEX_HOME": ""} {
		t.Setenv(k, v)
	}
	env := producer.EnvFilePath(home)
	if err := os.MkdirAll(filepath.Dir(env), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("EMBER_SOURCE=mbp\nEMBER_SERVER_URL="+srv.URL+"\nEMBER_TOKEN=tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestRunConfigureAndDoctor(t *testing.T) {
	home := entryHome(t)
	out := captureStdout(t, func() { runConfigure(nil) })
	if !strings.Contains(out, "Configure complete.") {
		t.Fatalf("configure output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "ember", "sessions")); err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	out = captureStdout(t, runDoctor)
	for _, want := range []string{"doctor:", `"mbp"`, "server: ", "(set, 3 chars)"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}
