package producer

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateOpenLogRedirectsDescriptors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	saved1, _ := unixDup(1)
	saved2, _ := unixDup(2)
	defer func() {
		unixRestore(saved1, 1)
		unixRestore(saved2, 2)
		os.Stdout, os.Stderr = oldOut, oldErr
	}()
	RedirectStandardIO(f)
	if _, err := os.Stdout.WriteString("before-rotation\n"); err != nil {
		t.Fatal(err)
	}
	if RotateOpenLog(path, 1<<30) {
		t.Fatal("rotated below threshold")
	}
	if !RotateOpenLog(path, 5) {
		t.Fatal("did not rotate past threshold")
	}
	if _, err := os.Stdout.WriteString("after-rotation\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stderr.WriteString("after-stderr\n"); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(path + ".1")
	cur, _ := os.ReadFile(path)
	if !strings.Contains(string(old), "before-rotation") || strings.Contains(string(old), "after") {
		t.Fatalf(".1 = %q", old)
	}
	if !strings.Contains(string(cur), "after-rotation") || !strings.Contains(string(cur), "after-stderr") || strings.Contains(string(cur), "before") {
		t.Fatalf("current = %q", cur)
	}
}

func unixDup(fd int) (int, error) { return unix.Dup(fd) }

func unixRestore(saved, fd int) {
	_ = unix.Dup2(saved, fd)
	_ = unix.Close(saved)
}
