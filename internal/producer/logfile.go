package producer

import (
	"os"
	"path/filepath"
	"syscall"
)

// OpenDaemonLog opens ~/Library/Logs/<name>.log for appending, creating the
// directory. Used instead of a plist StandardOutPath so a static bundled plist
// (which cannot encode a per-user path) still logs to the right place.
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

// RedirectStandardIO points both the OS-level stdout/stderr file descriptors
// (1 and 2) and the Go-level os.Stdout/os.Stderr variables at f. The fd-level
// dup2 is the part that matters for a KeepAlive daemon: an unrecovered panic
// or Go runtime fatal error is written by the runtime directly to fd 2,
// bypassing the os.Stderr variable entirely, so reassigning os.Stderr alone
// would silently lose crash output once the plist stops using
// StandardErrorPath. Dup2 failures are best-effort and never fatal — the
// daemon should keep running even if redirection couldn't be set up.
func RedirectStandardIO(f *os.File) {
	fd := int(f.Fd())
	_ = syscall.Dup2(fd, 1)
	_ = syscall.Dup2(fd, 2)
	os.Stdout = f
	os.Stderr = f
}
