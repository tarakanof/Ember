package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestReconcileTilesClearsLegacyUsageApps(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	c := app.coord

	c.tiles.adopt([]string{"ember-usage-claude-5h", "ember-usage-claude-7d"}, "ember")
	c.reconcileTiles(time.Now())
	if n := len(c.tiles.pushed); n != 0 {
		t.Fatalf("ledger not emptied: %d entries left", n)
	}
	cleared := pub.ClearedAppsSnapshot()
	if len(cleared) != 2 {
		t.Fatalf("cleared %d apps, want 2: %v", len(cleared), cleared)
	}
}

func TestUsageViewsThresholdGate(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	now := clk.Now()

	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 30, ResetLabel: "14:25"}, UpdatedAt: now})
	if v := c.usageViews(now, Snapshot{}); v["claude"] != nil {
		t.Fatalf("30%% < 60%%: want no view, got %+v", v["claude"])
	}

	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 87, ResetLabel: "17:30"},
		SevenDay:  &UsageWindow{UsedPercent: 42},
		Models:    map[string]*UsageWindow{"opus": {UsedPercent: 51}},
		UpdatedAt: now,
	})
	v := c.usageViews(now, Snapshot{})["claude"]
	if v == nil || v.FiveHourPct != 87 || v.ResetLabel != "17:30" {
		t.Fatalf("87%% >= 60%%: bad view %+v", v)
	}
	if v.SevenDayPct == nil || *v.SevenDayPct != 42 {
		t.Fatalf("7d missing: %+v", v)
	}
	if len(v.Models) != 1 || v.Models[0].Marker != "OP" || v.Models[0].Pct != 51 {
		t.Fatalf("models: %+v", v.Models)
	}
}

func TestUsageViewsStatuslineFallback(t *testing.T) {
	c, _, _, clk := makeAlarmCoord(t)
	now := clk.Now()
	pct := 90
	snap := Snapshot{Now: now, Sessions: []render.Session{{
		Source: "mbp", Tool: "claude", Session: "s", State: "running",
		RateWindowPct: &pct, RateResetAt: now.Add(time.Hour).Unix(),
		RateResetLabel: "13:00", UpdatedAt: now,
	}}}
	v := c.usageViews(now, snap)["claude"]
	if v == nil || v.FiveHourPct != 90 || v.ResetLabel != "13:00" {
		t.Fatalf("fallback view: %+v", v)
	}
	if v.SevenDayPct != nil {
		t.Fatal("statusline fallback has no 7d window")
	}
}

func TestUsageViewsWidgetOffYieldsNil(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	now := clk.Now()
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 99, ResetLabel: "17:30"}, UpdatedAt: now})
	cfg := *c.loadCfg()
	off := false
	cfg.UsageWidget = &off
	c.loadCfg = func() *Config { return &cfg }
	if v := c.usageViews(now, Snapshot{}); v != nil {
		t.Fatalf("widget off: want nil views, got %+v", v)
	}
}

func TestUsageViewsHiddenTool(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	now := clk.Now()
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 99, ResetLabel: "17:30"}, UpdatedAt: now})
	c.hiddenApps = func() map[string]bool { return map[string]bool{"claude": true} }
	if v := c.usageViews(now, Snapshot{})["claude"]; v != nil {
		t.Fatalf("hidden tool: want no view, got %+v", v)
	}
}

func TestIdleOverThresholdPublishesUsageFrame(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	cfg := *c.loadCfg()
	cfg.Display.IdleRestoreSeconds = 60
	c.loadCfg = func() *Config { return &cfg }

	now := clk.Now()
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 87, ResetLabel: "17:30"}, UpdatedAt: now})
	c.snapshot = func() Snapshot { return Snapshot{Now: clk.Now()} }

	c.onTick()
	clk.Advance(61 * time.Second)
	before := c.publishCount.Load()
	c.onTick()
	if c.publishCount.Load() == before {
		t.Fatal("idle over threshold: expected a usage-frame publish after countdown elapsed")
	}
}

func TestIdleUnderThresholdStopsPublishing(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	cfg := *c.loadCfg()
	cfg.Display.IdleRestoreSeconds = 60
	c.loadCfg = func() *Config { return &cfg }

	now := clk.Now()
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 30, ResetLabel: "17:30"}, UpdatedAt: now})
	c.snapshot = func() Snapshot { return Snapshot{Now: clk.Now()} }

	c.onTick()
	clk.Advance(61 * time.Second)
	before := c.publishCount.Load()
	c.onTick()
	if c.publishCount.Load() != before {
		t.Fatal("idle under threshold: expected NO publish (app should expire)")
	}
}

func TestIdleCardCursorAdvancesOnEmptyKeys(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	cfg := *c.loadCfg()
	cfg.Display.IdleRestoreSeconds = 60
	c.loadCfg = func() *Config { return &cfg }

	now := clk.Now()
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 87, ResetLabel: "17:30"},
		SevenDay:  &UsageWindow{UsedPercent: 55, ResetLabel: "MON"},
		UpdatedAt: now,
	})
	c.snapshot = func() Snapshot { return Snapshot{Now: clk.Now()} }

	c.onTick()
	c.stateMu.RLock()
	cursorAfterFirst := c.cardCursor
	c.stateMu.RUnlock()

	c.onTick()
	c.stateMu.RLock()
	cursorAfterSecond := c.cardCursor
	c.stateMu.RUnlock()

	if cursorAfterSecond != cursorAfterFirst+1 {
		t.Fatalf("cardCursor after two empty-keys ticks: %d → %d, want +1 increment",
			cursorAfterFirst, cursorAfterSecond)
	}
}
