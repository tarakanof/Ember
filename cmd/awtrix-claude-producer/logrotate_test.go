package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRotate_BelowThreshold_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	if err := os.WriteFile(path, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	rotateLogIfLarge(path, 1024)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("log should still exist: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Errorf(".1 should not exist (no rotation)")
	}
}

func TestRotate_AboveThreshold_RotatesAndKeeps5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	big := make([]byte, 100)
	for i := range big {
		big[i] = 'a'
	}
	for i := 0; i < 6; i++ {
		if err := os.WriteFile(path, big, 0o600); err != nil {
			t.Fatal(err)
		}
		rotateLogIfLarge(path, 50)
	}
	for i := 1; i <= 5; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Errorf(".%d should exist after 6 rotations", i)
		}
	}
	if _, err := os.Stat(path + ".6"); err == nil {
		t.Errorf(".6 should NOT exist (max 5 generations)")
	}
}
