package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const usageStaleTTL = 10 * time.Minute

var usageTools = []string{"claude", "codex"}

type usageToolState struct {
	Report     ToolUsage
	HaveReport bool
	Fresh      bool

	HaveFiveHour bool
	FiveHourPct  float64
	ResetAt      int64
	ResetLabel   string
}

type usageState struct {
	Tools map[string]usageToolState
}

func newUsageState(reports map[string]ToolUsage, sessions []render.Session, now time.Time) usageState {
	s := usageState{Tools: make(map[string]usageToolState, len(reports)+len(usageTools))}
	for tool, u := range reports {
		s.Tools[tool] = usageToolState{Report: u, HaveReport: true, Fresh: usageFresh(u, now)}
	}
	for _, tool := range usageTools {
		t := s.Tools[tool]
		if t.Fresh {
			if fh := t.Report.FiveHour; fh != nil {
				t.HaveFiveHour, t.FiveHourPct, t.ResetAt = true, fh.UsedPercent, fh.ResetsAt
				t.ResetLabel = fh.ResetLabel
			}
		} else {
			t.ResetLabel = latestResetLabel(sessions, tool)
		}
		if !t.HaveFiveHour {
			if best := latestRateWindow(sessions, tool); best != nil {
				t.HaveFiveHour, t.FiveHourPct, t.ResetAt = true, float64(*best.RateWindowPct), best.RateResetAt
			}
		}
		s.Tools[tool] = t
	}
	return s
}

func usageFresh(u ToolUsage, now time.Time) bool { return now.Sub(u.UpdatedAt) <= usageStaleTTL }

func (s *UsageStore) state(sessions []render.Session, now time.Time) usageState {
	return newUsageState(s.All(), sessions, now)
}

func latestRateWindow(sessions []render.Session, tool string) *render.Session {
	var best *render.Session
	for i := range sessions {
		s := &sessions[i]
		if s.Tool != tool || s.RateWindowPct == nil || s.RateResetAt == 0 {
			continue
		}
		if best == nil || s.UpdatedAt.After(best.UpdatedAt) {
			best = s
		}
	}
	return best
}

func latestResetLabel(sessions []render.Session, tool string) string {
	var best *render.Session
	for i := range sessions {
		s := &sessions[i]
		if s.Tool != tool || s.RateResetLabel == "" {
			continue
		}
		if best == nil || s.UpdatedAt.After(best.UpdatedAt) {
			best = s
		}
	}
	if best == nil {
		return ""
	}
	return best.RateResetLabel
}
