package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestUsageStateFreshness(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		age   time.Duration
		fresh bool
	}{
		{"now", 0, true},
		{"at the limit", usageStaleTTL, true},
		{"a second past", usageStaleTTL + time.Second, false},
		{"reported in the future", -time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newUsageState(map[string]ToolUsage{"claude": {UpdatedAt: now.Add(-tc.age)}}, nil, now)
			got := s.Tools["claude"]
			if !got.HaveReport || got.Fresh != tc.fresh {
				t.Fatalf("claude = %+v, want fresh %v", got, tc.fresh)
			}
		})
	}
}

func TestUsageStateFiveHour(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	pct := func(n int) *int { return &n }
	older := render.Session{Tool: "claude", RateWindowPct: pct(40), RateResetAt: 111, RateResetLabel: "11:00", UpdatedAt: now.Add(-time.Minute)}
	newer := render.Session{Tool: "claude", RateWindowPct: pct(70), RateResetAt: 222, RateResetLabel: "13:00", UpdatedAt: now}
	labelOnly := render.Session{Tool: "claude", RateResetLabel: "15:00", UpdatedAt: now.Add(time.Second)}
	other := render.Session{Tool: "codex", RateWindowPct: pct(90), RateResetAt: 333, UpdatedAt: now}
	report := func(age time.Duration, fh *UsageWindow) map[string]ToolUsage {
		return map[string]ToolUsage{"claude": {FiveHour: fh, UpdatedAt: now.Add(-age)}}
	}
	window := &UsageWindow{UsedPercent: 87.4, ResetsAt: 999, ResetLabel: "17:30"}
	cases := []struct {
		name     string
		reports  map[string]ToolUsage
		sessions []render.Session
		want     usageToolState
	}{
		{"fresh report wins over sessions", report(0, window), []render.Session{newer},
			usageToolState{HaveFiveHour: true, FiveHourPct: 87.4, ResetAt: 999, ResetLabel: "17:30"}},
		{"fresh report without a 5h window falls back for the numbers only", report(0, nil), []render.Session{newer},
			usageToolState{HaveFiveHour: true, FiveHourPct: 70, ResetAt: 222}},
		{"stale report uses the latest session", report(time.Hour, window), []render.Session{older, newer, other},
			usageToolState{HaveFiveHour: true, FiveHourPct: 70, ResetAt: 222, ResetLabel: "13:00"}},
		{"label comes from the latest labelled session", nil, []render.Session{newer, labelOnly},
			usageToolState{HaveFiveHour: true, FiveHourPct: 70, ResetAt: 222, ResetLabel: "15:00"}},
		{"nothing known", nil, []render.Session{other}, usageToolState{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newUsageState(tc.reports, tc.sessions, now).Tools["claude"]
			if got.HaveFiveHour != tc.want.HaveFiveHour || got.FiveHourPct != tc.want.FiveHourPct ||
				got.ResetAt != tc.want.ResetAt || got.ResetLabel != tc.want.ResetLabel {
				t.Fatalf("claude = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUsageStateKeepsUntrackedReports(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s := newUsageState(map[string]ToolUsage{"gemini": {FiveHour: &UsageWindow{UsedPercent: 50}, UpdatedAt: now}}, nil, now)
	g := s.Tools["gemini"]
	if !g.HaveReport || !g.Fresh || g.HaveFiveHour {
		t.Fatalf("gemini = %+v, want a fresh report without derived 5h", g)
	}
	for _, tool := range usageTools {
		if s.Tools[tool].HaveReport {
			t.Errorf("%s has a report", tool)
		}
	}
}
