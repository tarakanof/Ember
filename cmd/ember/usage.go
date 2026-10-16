package main

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	usageAccountTTL      = 7 * 24 * time.Hour
	usageAccountsPerTool = 8
	usageAccountsTotal   = 64
)

type UsageWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at,omitempty"`
	ResetLabel  string  `json:"reset_label,omitempty"`
}

type ToolUsage struct {
	FiveHour  *UsageWindow            `json:"five_hour,omitempty"`
	SevenDay  *UsageWindow            `json:"seven_day,omitempty"`
	Models    map[string]*UsageWindow `json:"models,omitempty"`
	Source    string                  `json:"source,omitempty"`
	Account   string                  `json:"account_key,omitempty"`
	Plan      string                  `json:"plan,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type usageKey struct {
	Tool    string
	Account string
}

func (k usageKey) id() string {
	if k.Account == "" {
		return k.Tool
	}
	return k.Tool + ":" + k.Account
}

type UsageStore struct {
	mu    sync.RWMutex
	byKey map[usageKey]ToolUsage
}

func newUsageStore() *UsageStore { return &UsageStore{byKey: map[usageKey]ToolUsage{}} }

var (
	errUsageNotOwned = errors.New("usage tool owned by another source")
	errUsageFull     = errors.New("usage account limit reached")
)

func (s *UsageStore) Put(tool string, u ToolUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.putLocked(tool, u, u.UpdatedAt)
}

func (s *UsageStore) expireLocked(now time.Time) {
	for key, other := range s.byKey {
		if now.Sub(other.UpdatedAt) > usageAccountTTL {
			delete(s.byKey, key)
		}
	}
}

func (s *UsageStore) putLocked(tool string, u ToolUsage, now time.Time) error {
	s.expireLocked(now)
	k := usageKey{tool, u.Account}
	prev, exists := s.byKey[k]
	if exists && u.UpdatedAt.Before(prev.UpdatedAt) {
		return nil
	}
	if !exists {
		s.evictOwnOldestLocked(tool, u.Source)
		if len(s.byKey) >= usageAccountsTotal {
			return errUsageFull
		}
	}
	s.byKey[k] = u
	return nil
}

func (s *UsageStore) evictOwnOldestLocked(tool, source string) {
	for {
		n, oldest, found := 0, usageKey{}, false
		for key, u := range s.byKey {
			if key.Tool != tool || u.Source != source {
				continue
			}
			n++
			if o := s.byKey[oldest]; !found || u.UpdatedAt.Before(o.UpdatedAt) {
				oldest, found = key, true
			}
		}
		if n < usageAccountsPerTool {
			return
		}
		delete(s.byKey, oldest)
	}
}

func (s *UsageStore) PutIfOwned(tool string, u ToolUsage, sources []string, now time.Time) (string, error) {
	u.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(now)
	if len(sources) > 0 {
		for k, prev := range s.byKey {
			if k.Tool != tool {
				continue
			}
			if owner := strings.TrimSpace(prev.Source); !slices.Contains(sources, owner) {
				return owner, errUsageNotOwned
			}
		}
	}
	return "", s.putLocked(tool, u, now)
}

func (s *UsageStore) Get(tool string) (ToolUsage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	var best usageToolState
	for k, u := range s.byKey {
		a := usageAccountState{Key: k, Report: u, Fresh: usageFresh(u, now)}
		if k.Tool == tool && (!best.HaveReport || usageOutranks(a, best)) {
			best = usageToolState{Report: u, HaveReport: true, Fresh: a.Fresh}
		}
	}
	return best.Report, best.HaveReport
}

func (s *UsageStore) All() map[usageKey]ToolUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.byKey)
}
