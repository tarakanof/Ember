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

type LinkState struct {
	OK      bool      `json:"ok"`
	NoRoute bool      `json:"no_route"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

type LinkStatus struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	last    *LinkState
	written bool
}

func NewLinkStatus(path string) *LinkStatus {
	return &LinkStatus{path: path, now: time.Now}
}

func LinkStatusPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ember", name+".link.json"), nil
}

func IsNoRoute(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EHOSTUNREACH) || strings.Contains(err.Error(), "no route to host")
}

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
