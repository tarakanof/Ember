package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePlistDaemonShape(t *testing.T) {
	out := string(generatePlist("/Users/x/go/bin/ember-t3-producer"))
	for _, want := range []string{
		"<string>com.ember.t3</string>",
		"<string>/Users/x/go/bin/ember-t3-producer</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"<string>run</string>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "StandardOutPath") || strings.Contains(out, "EMBER_TOKEN") {
		t.Error("plist must not set log paths or token material")
	}
}

func TestGeneratePlistEscapesPath(t *testing.T) {
	out := string(generatePlist("/Users/a&b/ember-t3-producer"))
	if !strings.Contains(out, "a&amp;b") {
		t.Fatalf("path not XML-escaped:\n%s", out)
	}
}

func TestConfigureCreatesDirsWithoutLaunchAgent(t *testing.T) {
	home := t.TempDir()
	if err := configureAt(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "ember", "producer.env")); err != nil {
		t.Fatalf("producer.env not seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "ember", "sessions")); err != nil {
		t.Fatalf("sessions dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")); !os.IsNotExist(err) {
		t.Fatal("configure must not write a LaunchAgent plist")
	}
}

func TestConfigureKeepsExistingEnv(t *testing.T) {
	home := t.TempDir()
	env := filepath.Join(home, ".config", "ember", "producer.env")
	if err := os.MkdirAll(filepath.Dir(env), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("EMBER_SOURCE=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configureAt(home); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(env); string(b) != "EMBER_SOURCE=keep\n" {
		t.Fatalf("producer.env overwritten: %q", b)
	}
}
