package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestHelpPrintsSubcommands(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "help")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("help should exit 0: %v\nstderr: %s", err, stderr.String())
	}
	for _, sub := range []string{"awtrix-menu", "run", "install", "uninstall", "doctor", "version"} {
		if !strings.Contains(stderr.String(), sub) {
			t.Errorf("help missing %q", sub)
		}
	}
}

func TestVersion_PrintsSomething(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "version")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("version should exit 0: %v", err)
	}
	if !strings.Contains(stdout.String(), "awtrix-menu") {
		t.Errorf("version output missing 'awtrix-menu': %q", stdout.String())
	}
}
