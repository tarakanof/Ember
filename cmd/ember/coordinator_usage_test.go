package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestUsageReconcilePicksVisibleFreshApps(t *testing.T) {
	st := newUsageStore()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	st.Put("claude", ToolUsage{
		FiveHour: &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"},
		SevenDay: &UsageWindow{UsedPercent: 72, ResetLabel: "MON"},
		Models:   map[string]*UsageWindow{"opus": {UsedPercent: 82, ResetLabel: "MON"}},
		Source:   "endpoint", UpdatedAt: now,
	})
	hidden := map[string]bool{} // nothing hidden
	apps := usageAppsToPush(st, hidden, true /*perModel*/, now, 10*time.Minute)
	names := map[string]bool{}
	for _, a := range apps {
		names[a.name] = true
	}
	for _, want := range []string{"ember-usage-claude-5h", "ember-usage-claude-7d", "ember-usage-claude-opus"} {
		if !names[want] {
			t.Errorf("missing app %s", want)
		}
	}
	// codex absent from store -> no codex apps
	if names["ember-usage-codex-5h"] {
		t.Error("codex should not be pushed")
	}
}

func TestUsageReconcileSkipsStaleAndHidden(t *testing.T) {
	st := newUsageStore()
	old := time.Date(2026, 6, 5, 11, 0, 0, 0, time.UTC)
	now := old.Add(30 * time.Minute) // stale
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"}, UpdatedAt: old})
	apps := usageAppsToPush(st, map[string]bool{}, true, now, 10*time.Minute)
	if len(apps) != 0 {
		t.Errorf("stale claude should yield no apps, got %d", len(apps))
	}

	// Fresh but hidden -> still no apps.
	st.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"}, UpdatedAt: now})
	apps = usageAppsToPush(st, map[string]bool{"claude": true}, true, now, 10*time.Minute)
	if len(apps) != 0 {
		t.Errorf("hidden claude should yield no apps, got %d", len(apps))
	}
}

func TestUsageReconcilePerModelToggleOff(t *testing.T) {
	st := newUsageStore()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"},
		Models:    map[string]*UsageWindow{"opus": {UsedPercent: 82, ResetLabel: "MON"}},
		UpdatedAt: now,
	})
	apps := usageAppsToPush(st, map[string]bool{}, false /*perModel off*/, now, 10*time.Minute)
	for _, a := range apps {
		if a.name == "ember-usage-claude-opus" {
			t.Error("per-model off should omit opus app")
		}
	}
}

func TestReconcileUsageAppsPushesAndClears(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	c := app.coord
	now := time.Now()

	// Fresh claude usage -> apps get pushed.
	app.usage.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"},
		SevenDay:  &UsageWindow{UsedPercent: 72, ResetLabel: "MON"},
		Models:    map[string]*UsageWindow{"opus": {UsedPercent: 82, ResetLabel: "MON"}},
		UpdatedAt: now,
	})
	c.reconcileUsageApps(now, Snapshot{})
	pushed := pub.CustomNamesSnapshot()
	if len(pushed) != 3 {
		t.Fatalf("first reconcile pushed %d apps, want 3: %v", len(pushed), pushed)
	}

	// Second reconcile, unchanged -> no additional pushes (payload dedup).
	c.reconcileUsageApps(now, Snapshot{})
	if got := len(pub.CustomNamesSnapshot()); got != 3 {
		t.Errorf("unchanged reconcile re-pushed: total %d, want 3", got)
	}

	// Usage goes stale -> previously-pushed apps get cleared.
	c.reconcileUsageApps(now.Add(30*time.Minute), Snapshot{})
	cleared := pub.ClearedAppsSnapshot()
	if len(cleared) != 3 {
		t.Errorf("stale reconcile cleared %d apps, want 3: %v", len(cleared), cleared)
	}
}

// TestReconcileUsageAppsRefreshesBeforeLifetimeExpiry guards the dedup/lifetime
// fix: an unchanged usage app must still be re-pushed once usageRefreshInterval
// elapses, so the device's lifetime never expires it without a refresh.
func TestReconcileUsageAppsRefreshesBeforeLifetimeExpiry(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	c := app.coord
	now := time.Now()
	app.usage.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"},
		UpdatedAt: now,
	})
	c.reconcileUsageApps(now, Snapshot{})
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Fatalf("first reconcile pushed %d, want 1", got)
	}
	// Unchanged + within the refresh interval -> no re-push (avoids churn).
	c.reconcileUsageApps(now.Add(usageRefreshInterval-time.Minute), Snapshot{})
	if got := len(pub.CustomNamesSnapshot()); got != 1 {
		t.Errorf("within refresh interval re-pushed: %d, want 1", got)
	}
	// Unchanged but past the refresh interval (store still fresh) -> re-push so
	// the device app is refreshed before its lifetime expires.
	c.reconcileUsageApps(now.Add(usageRefreshInterval+time.Minute), Snapshot{})
	if got := len(pub.CustomNamesSnapshot()); got != 2 {
		t.Errorf("past refresh interval should re-push unchanged app: %d, want 2", got)
	}
}

func TestReconcileUsageAppsDisabledClears(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	app := NewApp(cfg, pub, testLogger())
	c := app.coord
	now := time.Now()
	app.usage.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 15, ResetLabel: "14:25"}, UpdatedAt: now})
	c.reconcileUsageApps(now, Snapshot{})
	if len(pub.CustomNamesSnapshot()) == 0 {
		t.Fatal("expected apps pushed while enabled")
	}
	// Disable the widget and reconcile -> pushed apps are cleared.
	disabled := false
	newCfg := *app.cfg.Load()
	newCfg.UsageWidget = &disabled
	app.cfg.Store(&newCfg)
	c.reconcileUsageApps(now, Snapshot{})
	if len(pub.ClearedAppsSnapshot()) == 0 {
		t.Error("disabling the widget should clear pushed apps")
	}
}

func TestClaudeFallbackApp(t *testing.T) {
	now := time.Now()
	pct := 64
	// No usable session -> ok=false.
	if _, ok := claudeFallbackApp(Snapshot{Sessions: []Session{
		{Tool: "codex", RateWindowPct: &pct, RateResetLabel: "14:25"}, // wrong tool
		{Tool: "claude", RateWindowPct: nil, RateResetLabel: "14:25"}, // no pct
		{Tool: "claude", RateWindowPct: &pct, RateResetLabel: ""},     // no label
	}}); ok {
		t.Error("no usable claude session should yield ok=false")
	}
	// Most-recent usable claude session wins.
	older := 10
	newer := 64
	app, ok := claudeFallbackApp(Snapshot{Sessions: []Session{
		{Tool: "claude", RateWindowPct: &older, RateResetLabel: "09:00", UpdatedAt: now.Add(-time.Hour)},
		{Tool: "claude", RateWindowPct: &newer, RateResetLabel: "14:25", UpdatedAt: now},
	}})
	if !ok {
		t.Fatal("usable claude session should yield ok=true")
	}
	if app.name != "ember-usage-claude-5h" {
		t.Errorf("name = %q", app.name)
	}
	if _, hasText := app.payload["text"]; hasText {
		t.Error("5h fallback is fully drawn, no native text")
	}
}

func TestReconcileUsesFallbackWhenEndpointStale(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	c := app.coord
	now := time.Now()
	// No endpoint usage in the store; a live claude session carries statusline data.
	pct := 64
	snap := Snapshot{Sessions: []Session{
		{Source: "mbp", Tool: "claude", Session: "s1", State: "running",
			RateWindowPct: &pct, RateResetLabel: "14:25", UpdatedAt: now},
	}}
	c.reconcileUsageApps(now, snap)
	pushed := pub.CustomNamesSnapshot()
	if len(pushed) != 1 || pushed[0] != "ember-usage-claude-5h" {
		t.Fatalf("fallback should push exactly the claude-5h app, got %v", pushed)
	}

	// Once authoritative endpoint usage arrives, it supersedes the fallback
	// (still a single claude-5h app, now from the endpoint) and adds 7d.
	app.usage.Put("claude", ToolUsage{
		FiveHour: &UsageWindow{UsedPercent: 15, ResetLabel: "15:00"},
		SevenDay: &UsageWindow{UsedPercent: 72, ResetLabel: "MON"},
		Source:   "endpoint", UpdatedAt: now,
	})
	c.reconcileUsageApps(now, snap)
	names := map[string]int{}
	for _, n := range pub.CustomNamesSnapshot() {
		names[n]++
	}
	if names["ember-usage-claude-7d"] == 0 {
		t.Error("endpoint usage should add the 7d app")
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
