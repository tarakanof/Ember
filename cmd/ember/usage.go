package main

import (
	"sync"
	"time"
)

// UsageWindow mirrors producer.UsageWindow on the server side.
type UsageWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at,omitempty"`
	ResetLabel  string  `json:"reset_label,omitempty"`
}

// ToolUsage is the latest usage snapshot for one tool.
type ToolUsage struct {
	FiveHour  *UsageWindow            `json:"five_hour,omitempty"`
	SevenDay  *UsageWindow            `json:"seven_day,omitempty"`
	Models    map[string]*UsageWindow `json:"models,omitempty"`
	Source    string                  `json:"source,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
}

// UsageStore is an in-memory, concurrency-safe per-tool usage cache. It is
// deliberately NOT persisted: every entry refreshes on a <=5-min cadence, so a
// restart self-heals within one interval.
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

func (s *UsageStore) Get(tool string) (ToolUsage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.byTool[tool]
	return u, ok
}

// Fresh reports whether tool's entry was updated within ttl before now.
func (s *UsageStore) Fresh(tool string, now time.Time, ttl time.Duration) bool {
	u, ok := s.Get(tool)
	if !ok {
		return false
	}
	return now.Sub(u.UpdatedAt) <= ttl
}
