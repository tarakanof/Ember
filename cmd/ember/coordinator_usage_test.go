package main

import (
	"testing"
	"time"
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
