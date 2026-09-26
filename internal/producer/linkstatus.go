package producer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LinkState is what a LinkStatus file holds: whether the daemon's last
// request reached the server. The macOS app reads it to tell the user when
// macOS blocks the helper's Local Network access, which otherwise only shows
// as "connect: no route to host" in the daemon log.
type LinkState struct {
	// OK: the last request got an HTTP response (any status).
	OK bool `json:"ok"`
	// NoRoute: the last request failed with EHOSTUNREACH, which is how
	// Local Network privacy denies a LAN connection.
	NoRoute bool `json:"no_route"`
	// Error: the last transport error, empty when OK.
	Error string `json:"error,omitempty"`
	// At: when this state began.
	At time.Time `json:"at"`
}

// LinkStatus records a daemon's link state in a small JSON file, rewriting
// it only when the state changes. Hooks run in the user's terminal, which
// has its own Local Network permission, so only the LaunchAgent records.
// A nil *LinkStatus records nothing.
type LinkStatus struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	last    *LinkState
	written bool
}

// NewLinkStatus records into path.
func NewLinkStatus(path string) *LinkStatus {
	return &LinkStatus{path: path, now: time.Now}
}

// LinkStatusPath is where the named daemon ("claude-producer",
// "codex-producer") records its link state: next to producer.env.
func LinkStatusPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ember", name+".link.json"), nil
}

// IsNoRoute reports whether err is EHOSTUNREACH ("no route to host").
func IsNoRoute(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EHOSTUNREACH) || strings.Contains(err.Error(), "no route to host")
}

// Record notes the result of one request: nil for any HTTP response, the
// transport error otherwise. Best effort: a write failure is ignored and
// retried on the next change.
func (l *LinkStatus) Record(err error) {
	if l == nil {
		return
	}
	next := LinkState{OK: err == nil, NoRoute: IsNoRoute(err)}
	if err != nil {
		next.Error = err.Error()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.written && l.last.OK == next.OK && l.last.NoRoute == next.NoRoute {
		return
	}
	next.At = l.now()
	l.last = &next
	l.written = l.write(next) == nil
}

func (l *LinkStatus) write(s LinkState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}
