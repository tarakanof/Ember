package producer

import (
	"os"
	"path/filepath"
	"syscall"
)

// OpenDaemonLog opens ~/Library/Logs/<name>.log for appending, creating the directory.
func OpenDaemonLog(name string) (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "Library", "Logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// RedirectStandardIO points the stdout/stderr file descriptors and os.Stdout/os.Stderr at f.
func RedirectStandardIO(f *os.File) {
	fd := int(f.Fd())
	_ = syscall.Dup2(fd, 1)
	_ = syscall.Dup2(fd, 2)
	os.Stdout = f
	os.Stderr = f
}
