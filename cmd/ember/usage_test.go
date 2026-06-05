package main

import (
	"testing"
	"time"
)

func TestUsageStorePutGetFresh(t *testing.T) {
	s := newUsageStore()
	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	s.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"},
		Source:    "endpoint",
		UpdatedAt: base,
	})
	got, ok := s.Get("claude")
	if !ok || got.FiveHour == nil || got.FiveHour.ResetLabel != "14:25" {
		t.Fatalf("Get = %+v ok=%v", got, ok)
	}
	if !s.Fresh("claude", base.Add(9*time.Minute), 10*time.Minute) {
		t.Error("should be fresh within ttl")
	}
	if s.Fresh("claude", base.Add(11*time.Minute), 10*time.Minute) {
		t.Error("should be stale past ttl")
	}
	if _, ok := s.Get("codex"); ok {
		t.Error("codex should be absent")
	}
}
