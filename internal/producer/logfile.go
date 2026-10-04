package producer

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// StateHome is $XDG_STATE_HOME when set to an absolute path, else
// ~/.local/state (the XDG default). Session markers keep their fixed
// ~/.local/state/ember/sessions path, shared with Ember.app.
func StateHome(home string) string {
	if v := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(home, ".local", "state")
}

// LogDir is where producer logs live: ~/Library/Logs on macOS,
// $XDG_STATE_HOME/ember/logs elsewhere.
func LogDir(home string) string {
	return logDirFor(runtime.GOOS, home)
}

func logDirFor(goos, home string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Logs")
	}
	return filepath.Join(StateHome(home), "ember", "logs")
}

// LogDirShell is LogDir as a shell expression for commands written into
// settings.json (hook and statusline log redirects), expanded by the hook's
// own environment like LogDir is.
func LogDirShell() string {
	return logDirShellFor(runtime.GOOS)
}

func logDirShellFor(goos string) string {
	if goos == "darwin" {
		return "$HOME/Library/Logs"
	}
	return "${XDG_STATE_HOME:-$HOME/.local/state}/ember/logs"
}

// LogPath is the log file for name ("ember-codex-producer") under LogDir.
func LogPath(home, name string) string {
	return filepath.Join(LogDir(home), name+".log")
}

// OpenDaemonLog opens LogDir/<name>.log for appending, creating the directory.
func OpenDaemonLog(name string) (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := LogDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// RedirectStandardIO points the stdout/stderr file descriptors and os.Stdout/os.Stderr at f.
// unix.Dup2 (not syscall.Dup2) because linux/arm64 has only dup3.
func RedirectStandardIO(f *os.File) {
	fd := int(f.Fd())
	_ = unix.Dup2(fd, 1)
	_ = unix.Dup2(fd, 2)
	os.Stdout = f
	os.Stderr = f
}
