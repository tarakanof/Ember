package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestHelpSubcommandPrintsUsage(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "help")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("help should exit 0: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ember-claude-producer") {
		t.Errorf("help output missing program name: %q", stderr.String())
	}
	for _, sub := range []string{"hook", "tick", "install", "uninstall", "configure", "deconfigure", "doctor"} {
		if !strings.Contains(stderr.String(), sub) {
			t.Errorf("help missing %q subcommand", sub)
		}
	}
}

func TestUnknownSubcommandExitsNonZero(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "bogus")
	if err := cmd.Run(); err == nil {
		t.Fatal("bogus subcommand should exit non-zero")
	}
}
