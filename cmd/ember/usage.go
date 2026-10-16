package main

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

const usageAccountTTL = 7 * 24 * time.Hour

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
	return k.Account
}

type UsageStore struct {
	mu    sync.RWMutex
	byKey map[usageKey]ToolUsage
}

func newUsageStore() *UsageStore { return &UsageStore{byKey: map[usageKey]ToolUsage{}} }

func (s *UsageStore) Put(tool string, u ToolUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(tool, u)
}

func (s *UsageStore) putLocked(tool string, u ToolUsage) {
	k := usageKey{tool, u.Account}
	if prev, ok := s.byKey[k]; ok && u.UpdatedAt.Before(prev.UpdatedAt) {
		return
	}
	s.byKey[k] = u
	for key, other := range s.byKey {
		if u.UpdatedAt.Sub(other.UpdatedAt) > usageAccountTTL {
			delete(s.byKey, key)
		}
	}
}

func (s *UsageStore) PutIfOwned(tool string, u ToolUsage, sources []string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.byKey[usageKey{tool, u.Account}]; ok && len(sources) > 0 {
		owner := strings.TrimSpace(prev.Source)
		if !slices.Contains(sources, owner) {
			return owner, false
		}
	}
	s.putLocked(tool, u)
	return "", true
}

func (s *UsageStore) Get(tool string) (ToolUsage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best ToolUsage
	found := false
	for k, u := range s.byKey {
		if k.Tool == tool && (!found || u.UpdatedAt.After(best.UpdatedAt)) {
			best, found = u, true
		}
	}
	return best, found
}

func (s *UsageStore) All() map[usageKey]ToolUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.byKey)
}
