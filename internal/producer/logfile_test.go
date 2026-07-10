package producer

import (
	"os"
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
