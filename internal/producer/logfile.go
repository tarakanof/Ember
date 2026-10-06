package producer

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

func StateHome(home string) string {
	if v := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(home, ".local", "state")
}

func LogDir(home string) string {
	return logDirFor(runtime.GOOS, home)
}

func logDirFor(goos, home string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Logs")
	}
	return filepath.Join(StateHome(home), "ember", "logs")
}

func LogDirShell() string {
	return logDirShellFor(runtime.GOOS)
}

func logDirShellFor(goos string) string {
	if goos == "darwin" {
		return "$HOME/Library/Logs"
	}
	return "${XDG_STATE_HOME:-$HOME/.local/state}/ember/logs"
}

func LogPath(home, name string) string {
	return filepath.Join(LogDir(home), name+".log")
}

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

// unix.Dup2, not syscall.Dup2: linux/arm64 has only dup3.
func RedirectStandardIO(f *os.File) {
	fd := int(f.Fd())
	_ = unix.Dup2(fd, 1)
	_ = unix.Dup2(fd, 2)
	os.Stdout = f
	os.Stderr = f
}

func RotateLogs(names ...string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, n := range names {
		RotateLogIfLarge(LogPath(home, n), DefaultLogThreshold)
	}
}

func StartDaemonLog(name string, alsoRotate ...string) {
	RotateLogs(append([]string{name}, alsoRotate...)...)
	f, err := OpenDaemonLog(name)
	if err != nil {
		return
	}
	RedirectStandardIO(f)
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}
