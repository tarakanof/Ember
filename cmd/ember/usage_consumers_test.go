package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestUsageConsumersAgreeOnFreshness(t *testing.T) {
	cases := []struct {
		name  string
		age   time.Duration
		fresh bool
	}{
		{"just reported", 0, true},
		{"just under the limit", usageStaleTTL - time.Second, true},
		{"at the limit", usageStaleTTL, true},
		{"just past the limit", usageStaleTTL + time.Second, false},
		{"long stale", 5 * time.Hour, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, st, _, clk := makeAlarmCoord(t)
			now := clk.Now()
			storeReset := now.Add(time.Hour).Unix()
			st.Put("claude", ToolUsage{
				FiveHour:  &UsageWindow{UsedPercent: 99.8, ResetsAt: storeReset, ResetLabel: "17:30"},
				SevenDay:  &UsageWindow{UsedPercent: 42},
				Source:    "m4",
				UpdatedAt: now.Add(-tc.age),
			})
			sessionPct := 70
			snap := Snapshot{Now: now, Sessions: []render.Session{{
				Source: "m4", Tool: "claude", Session: "s", State: "running",
				RateWindowPct: &sessionPct, RateResetAt: now.Add(2 * time.Hour).Unix(),
				RateResetLabel: "13:00", UpdatedAt: now,
			}}}

			v := c.usageViews(now, snap)["claude"]
			if v == nil {
				t.Fatal("no clock usage view")
			}
			if tc.fresh {
				if v.FiveHourPct != 100 || v.ResetLabel != "17:30" || v.SevenDayPct == nil || *v.SevenDayPct != 42 {
					t.Errorf("clock view = %+v, want the reported 5h/7d", v)
				}
			} else if v.FiveHourPct != 70 || v.ResetLabel != "13:00" || v.SevenDayPct != nil {
				t.Errorf("clock view = %+v, want the statusline fallback", v)
			}

			app := &App{usage: st}
			dash := app.buildUsageSnapshot(now)
			if len(dash.Tools) != 1 || dash.Tools[0].Stale == tc.fresh {
				t.Errorf("dashboard tools = %+v, want stale %v", dash.Tools, !tc.fresh)
			}
			if dash.StaleAfterSec != int(usageStaleTTL/time.Second) {
				t.Errorf("stale_after_sec = %d", dash.StaleAfterSec)
			}

			c.checkLimitAlarms(now, snap)
			armed, ok := c.alarmArmed["claude"]
			if tc.fresh && (!ok || armed != storeReset) {
				t.Errorf("alarm armed = %v %v, want the reported reset %v", armed, ok, storeReset)
			}
			if !tc.fresh && ok {
				t.Errorf("alarm armed at %v on the 70%% statusline fallback", armed)
			}
		})
	}
}
