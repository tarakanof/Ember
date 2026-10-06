package main

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
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
	UpdatedAt time.Time               `json:"updated_at"`
}

type UsageStore struct {
	mu     sync.RWMutex
	byTool map[string]ToolUsage
}

func newUsageStore() *UsageStore { return &UsageStore{byTool: map[string]ToolUsage{}} }

func (s *UsageStore) Put(tool string, u ToolUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTool[tool] = u
}

func (s *UsageStore) PutIfOwned(tool string, u ToolUsage, sources []string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.byTool[tool]; ok && len(sources) > 0 {
		owner := strings.TrimSpace(prev.Source)
		if !slices.Contains(sources, owner) {
			return owner, false
		}
	}
	s.byTool[tool] = u
	return "", true
}

func (s *UsageStore) Get(tool string) (ToolUsage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.byTool[tool]
	return u, ok
}

func (s *UsageStore) All() map[string]ToolUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.byTool)
}

func (s *UsageStore) Fresh(tool string, now time.Time, ttl time.Duration) bool {
	u, ok := s.Get(tool)
	if !ok {
		return false
	}
	return now.Sub(u.UpdatedAt) <= ttl
}
