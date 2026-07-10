package producer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenDaemonLog_CreatesAppendableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f, err := OpenDaemonLog("ember-tick")
	if err != nil {
		t.Fatalf("OpenDaemonLog: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(home, "Library", "Logs", "ember-tick.log"))
	if !strings.Contains(string(got), "hello") {
		t.Fatalf("log not written to ~/Library/Logs/ember-tick.log")
	}
}

func TestOpenDaemonLog_AppendsAcrossCalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f1, err := OpenDaemonLog("ember-codex-producer")
	if err != nil {
		t.Fatalf("OpenDaemonLog (first): %v", err)
	}
	if _, err := f1.WriteString("first\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f1.Close()

	f2, err := OpenDaemonLog("ember-codex-producer")
	if err != nil {
		t.Fatalf("OpenDaemonLog (second): %v", err)
	}
	defer f2.Close()
	if _, err := f2.WriteString("second\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(home, "Library", "Logs", "ember-codex-producer.log"))
	if !strings.Contains(string(got), "first") || !strings.Contains(string(got), "second") {
		t.Fatalf("expected both writes appended, got: %q", got)
	}
}

// TestRedirectStandardIO_CatchesRuntimePanic proves the regression this
// function fixes: an unrecovered panic is written by the Go runtime directly
// to fd 2, bypassing the os.Stderr variable entirely. Reassigning
// os.Stderr alone (the pre-fix behavior) would lose that output once the
// plist stops using StandardErrorPath; only an fd-level dup2 catches it.
//
// This re-execs the test binary as a subprocess (the standard Go pattern for
// testing crash/exit behavior) so the panic's process teardown doesn't kill
// the test runner itself.
func TestRedirectStandardIO_CatchesRuntimePanic(t *testing.T) {
	if os.Getenv("EMBER_LOGFD_CRASH") == "1" {
		f, err := OpenDaemonLog("crash-test")
		if err != nil {
			os.Exit(2)
		}
		RedirectStandardIO(f)
		panic("boom")
	}

	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRedirectStandardIO_CatchesRuntimePanic$")
	cmd.Env = append(os.Environ(), "EMBER_LOGFD_CRASH=1", "HOME="+home)
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected subprocess to exit non-zero from the panic")
	}

	got, err := os.ReadFile(filepath.Join(home, "Library", "Logs", "crash-test.log"))
	if err != nil {
		t.Fatalf("reading crash log: %v", err)
	}
	if !strings.Contains(string(got), "panic: boom") {
		t.Fatalf("expected panic output redirected into log file, got: %q", got)
	}
}
