package producer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotateLogIfLarge_RotatesOverThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	RotateLogIfLarge(path, 5) // 10 bytes > 5
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected rotated file x.log.1, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("original should be renamed away, stat err = %v", err)
	}
}

func TestRotateLogIfLarge_NoopUnderThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	RotateLogIfLarge(path, 1024)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should be untouched, got %v", err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("should not rotate under threshold")
	}
}
