package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProducerBin_FindsInGOBIN(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "awtrix-claude-producer")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", dir)
	t.Setenv("GOPATH", "")
	t.Setenv("PATH", "")

	if got := findProducerBin(); got != target {
		t.Errorf("findProducerBin = %q, want %q", got, target)
	}
}

func TestFindProducerBin_FindsInGOPATHBin(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(binDir, "awtrix-claude-producer")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", dir)
	t.Setenv("PATH", "")

	if got := findProducerBin(); got != target {
		t.Errorf("findProducerBin = %q, want %q", got, target)
	}
}

func TestFindProducerBin_NotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOBIN", dir)
	t.Setenv("GOPATH", "")
	t.Setenv("HOME", dir)
	t.Setenv("PATH", "")

	if got := findProducerBin(); got != "" {
		t.Errorf("findProducerBin = %q, want \"\"", got)
	}
}
