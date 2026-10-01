package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestVersion_PrintsBinaryName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "version")
	cmd.Env = append(cmd.Environ(), "CONFIG_PATH=/nonexistent/awtrix.json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("`go run . version` failed: %v\nstderr: %s", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "ember") {
		t.Errorf("version output missing binary name; got %q", out)
	}
	if !strings.Contains(out, "go1.") {
		t.Errorf("version output missing go runtime tag; got %q", out)
	}
}
