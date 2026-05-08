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
	for _, sub := range []string{"awtrix-menu", "run", "install", "uninstall", "doctor"} {
		if !strings.Contains(stderr.String(), sub) {
			t.Errorf("help missing %q", sub)
		}
	}
}
