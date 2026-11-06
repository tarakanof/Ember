package main

import (
	"testing"
	"time"
)

func TestUsageStoreKeepsAPostAndAgesItStale(t *testing.T) {
	s := newUsageStore()
	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	putUsage(t, s, "claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"},
		Source:    "endpoint",
		UpdatedAt: base,
	})
	got, ok := toolUsage(s, "claude", base)
	if !ok || got.FiveHour == nil || got.FiveHour.ResetLabel != "14:25" {
		t.Fatalf("tool usage = %+v ok=%v", got, ok)
	}
	if !s.state(nil, base.Add(9*time.Minute)).Tools["claude"].Fresh {
		t.Error("should be fresh within ttl")
	}
	if s.state(nil, base.Add(11*time.Minute)).Tools["claude"].Fresh {
		t.Error("should be stale past ttl")
	}
	if _, ok := toolUsage(s, "codex", base); ok {
		t.Error("codex should be absent")
	}
}

func putUsage(t *testing.T, s *UsageStore, tool string, u ToolUsage) {
	t.Helper()
	if _, err := s.PutIfOwned(tool, u, nil, 0, u.UpdatedAt); err != nil {
		t.Fatalf("put %s usage: %v", tool, err)
	}
}

func toolUsage(s *UsageStore, tool string, now time.Time) (ToolUsage, bool) {
	ts := s.state(nil, now).Tools[tool]
	return ts.Report, ts.HaveReport
}
