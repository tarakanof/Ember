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
	for _, want := range []string{"ember-t3-producer", "run", "install", "uninstall", "configure", "deconfigure", "doctor"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestUnknownSubcommandExitsNonZero(t *testing.T) {
	if err := exec.Command("go", "run", ".", "bogus").Run(); err == nil {
		t.Fatal("bogus subcommand should exit non-zero")
	}
}
