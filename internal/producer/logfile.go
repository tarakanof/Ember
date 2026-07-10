package producer

import (
	"os"
	"path/filepath"
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
