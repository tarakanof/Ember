package main

import (
	"cmp"
	"slices"
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

type usageAccountState struct {
	Key    usageKey
	Report ToolUsage
	Fresh  bool
}

type usageState struct {
	Tools    map[string]usageToolState
	Accounts []usageAccountState
}

func newUsageState(reports map[usageKey]ToolUsage, sessions []render.Session, now time.Time) usageState {
	s := usageState{Tools: make(map[string]usageToolState, len(reports)+len(usageTools))}
	for k, u := range reports {
		if now.Sub(u.UpdatedAt) > usageAccountTTL {
			continue
		}
		a := usageAccountState{Key: k, Report: u, Fresh: usageFresh(u, now)}
		s.Accounts = append(s.Accounts, a)
		if cur, ok := s.Tools[k.Tool]; !ok || usageOutranks(a, cur) {
			s.Tools[k.Tool] = usageToolState{Report: u, HaveReport: true, Fresh: a.Fresh}
		}
	}
	slices.SortFunc(s.Accounts, func(x, y usageAccountState) int {
		return cmp.Or(cmp.Compare(x.Key.Tool, y.Key.Tool), cmp.Compare(x.Key.Account, y.Key.Account))
	})
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

func usageOutranks(a usageAccountState, cur usageToolState) bool {
	if a.Fresh != cur.Fresh {
		return a.Fresh
	}
	if a.Fresh {
		if p, q := fiveHourOrNone(a.Report), fiveHourOrNone(cur.Report); p != q {
			return p > q
		}
	}
	if !a.Report.UpdatedAt.Equal(cur.Report.UpdatedAt) {
		return a.Report.UpdatedAt.After(cur.Report.UpdatedAt)
	}
	return a.Report.Account < cur.Report.Account
}

func fiveHourOrNone(u ToolUsage) float64 {
	if u.FiveHour == nil {
		return -1
	}
	return u.FiveHour.UsedPercent
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
