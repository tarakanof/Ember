package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestClearLegacyUsageApps(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	c := app.coord

	// Simulate adoptDeviceManagedApps having found leftovers from an old run.
	c.pushedUsageApps = map[string]pushedUsageApp{
		"ember-usage-claude-5h": {},
		"ember-usage-claude-7d": {},
	}
	c.clearLegacyUsageApps()
	if n := len(c.pushedUsageApps); n != 0 {
		t.Fatalf("tracker not emptied: %d entries left", n)
	}
	cleared := pub.ClearedAppsSnapshot()
	if len(cleared) != 2 {
		t.Fatalf("cleared %d apps, want 2: %v", len(cleared), cleared)
	}
}

func TestUsageViewsThresholdGate(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	now := clk.Now()

	// Below threshold (default 60) -> no view.
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 30, ResetLabel: "14:25"}, UpdatedAt: now})
	if v := c.usageViews(now, Snapshot{}); v["claude"] != nil {
		t.Fatalf("30%% < 60%%: want no view, got %+v", v["claude"])
	}

	// Over threshold -> view with label, 7d, models (per-model default on).
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
	// No endpoint usage; a live claude session reports 90% + a reset label.
	// effectiveFiveHour's fallback requires RateResetAt != 0 to accept a session.
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
	// Fresh hot usage for claude, but claude is hidden from the device display.
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 99, ResetLabel: "17:30"}, UpdatedAt: now})
	c.hiddenApps = func() map[string]bool { return map[string]bool{"claude": true} }
	if v := c.usageViews(now, Snapshot{})["claude"]; v != nil {
		t.Fatalf("hidden tool: want no view, got %+v", v)
	}
}

// TestIdleOverThresholdPublishesUsageFrame verifies that when the idle countdown
// elapses and at least one tool's 5h usage is over the threshold, the coordinator
// publishes the dimmed usage frame instead of letting the device lifetime expire.
func TestIdleOverThresholdPublishesUsageFrame(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	// Override IdleRestoreSeconds to something short so we don't have to
	// advance 120s (still within usageStaleTTL=10min, but this is cleaner).
	cfg := *c.loadCfg()
	cfg.Display.IdleRestoreSeconds = 60
	c.loadCfg = func() *Config { return &cfg }

	now := clk.Now()
	// Fresh claude usage over threshold (default 60%). Store freshness is based
	// on UpdatedAt; the data must still be within usageStaleTTL=10min at the
	// time of the second tick (61s later — well within 600s).
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 87, ResetLabel: "17:30"}, UpdatedAt: now})
	c.snapshot = func() Snapshot { return Snapshot{Now: clk.Now()} } // no sessions → idle

	// First tick: starts the idle countdown (idleModeDimmed → publishes dim frame).
	c.onTick()
	// Advance past the idle countdown.
	clk.Advance(61 * time.Second)
	before := c.publishCount.Load()
	// Second tick: countdown elapsed → idleModeOff. Over-threshold usage must
	// cause the coordinator to publish the dimmed usage frame (not return early).
	c.onTick()
	if c.publishCount.Load() == before {
		t.Fatal("idle over threshold: expected a usage-frame publish after countdown elapsed")
	}
}

// TestIdleUnderThresholdStopsPublishing verifies that when the idle countdown
// elapses and no tool's 5h usage is over the threshold, the coordinator does NOT
// publish — the app lifetime expires and AWTRIX returns to native apps.
func TestIdleUnderThresholdStopsPublishing(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	cfg := *c.loadCfg()
	cfg.Display.IdleRestoreSeconds = 60
	c.loadCfg = func() *Config { return &cfg }

	now := clk.Now()
	// Usage at 30% — below the default 60% threshold. The view is absent,
	// so RenderIdleUsagePayload returns nil and publish bails out as before.
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

// TestIdleCardCursorAdvancesOnEmptyKeys verifies that consecutive dwell ticks
// with no active sessions increment cardCursor, which drives face rotation in
// RenderIdleUsagePayload (cursor wraps inside render).
func TestIdleCardCursorAdvancesOnEmptyKeys(t *testing.T) {
	c, st, _, clk := makeAlarmCoord(t)
	cfg := *c.loadCfg()
	cfg.Display.IdleRestoreSeconds = 60
	c.loadCfg = func() *Config { return &cfg }

	now := clk.Now()
	// Claude with both 5h and 7d gives two faces so cursor rotation is meaningful.
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 87, ResetLabel: "17:30"},
		SevenDay:  &UsageWindow{UsedPercent: 55, ResetLabel: "MON"},
		UpdatedAt: now,
	})
	c.snapshot = func() Snapshot { return Snapshot{Now: clk.Now()} }

	// First tick starts the idle countdown; cardCursor goes 0→1.
	c.onTick()
	c.muTest.RLock()
	cursorAfterFirst := c.cardCursor
	c.muTest.RUnlock()

	c.onTick()
	c.muTest.RLock()
	cursorAfterSecond := c.cardCursor
	c.muTest.RUnlock()

	if cursorAfterSecond != cursorAfterFirst+1 {
		t.Fatalf("cardCursor after two empty-keys ticks: %d → %d, want +1 increment",
			cursorAfterFirst, cursorAfterSecond)
	}
}
