package main

import (
	"context"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestCoordinatorPomodoroPreemptsAndTakesOver(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), nil)
	c.ctx = context.Background()

	snap := Snapshot{Now: time.Now()}
	c.snapshot = func() Snapshot { return snap }

	active := false
	c.pomoView = func() (render.PomodoroView, bool) {
		if !active {
			return render.PomodoroView{}, false
		}
		return render.PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500, FocusColor: render.RGB{R: 0xff}}, true
	}

	// Inactive: normal path, no device-takeover settings written.
	c.publish(snap)
	if n := len(pub.SettingsSnapshot()); n != 0 {
		t.Fatalf("settings before active = %d, want 0", n)
	}

	// Activate → pomodoro frame published, takeover settings + switch fire once.
	active = true
	c.publish(snap)
	apps := pub.CustomAppsSnapshot()
	if len(apps) == 0 {
		t.Fatal("expected a published custom app")
	}
	// Pomodoro now publishes a built-in animated icon payload (tomato/coffee)
	// rather than a drawn db frame.
	if got := apps[len(apps)-1]["icon"]; got != "29802" && got != "6396" {
		t.Fatalf("expected pomodoro built-in icon payload, got %+v", apps[len(apps)-1])
	}
	s := pub.SettingsSnapshot()
	if len(s) != 1 || s[0]["ATRANS"] != false || s[0]["BLOCKN"] != true {
		t.Fatalf("takeover settings = %+v, want ATRANS:false BLOCKN:true once", s)
	}
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 || sw[0] != cfg.AWTRIX.AppName {
		t.Fatalf("switch = %+v, want [%s]", sw, cfg.AWTRIX.AppName)
	}

	// Still active: takeover is edge-triggered, not repeated.
	c.publish(snap)
	if n := len(pub.SettingsSnapshot()); n != 1 {
		t.Fatalf("settings while still active = %d, want 1 (edge only)", n)
	}

	// Deactivate → rotation/nav restored once.
	active = false
	c.publish(snap)
	s = pub.SettingsSnapshot()
	if len(s) != 2 || s[1]["ATRANS"] != true || s[1]["BLOCKN"] != false {
		t.Fatalf("restore settings = %+v, want ATRANS:true BLOCKN:false", s)
	}
}

func TestCoordinatorReassertsTakeoverPeriodically(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	clk := &fakeClock{now: time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)}
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, clk, testLogger(), nil)
	c.ctx = context.Background()

	snap := Snapshot{Now: clk.Now()}
	c.snapshot = func() Snapshot { return snap }
	c.pomoView = func() (render.PomodoroView, bool) {
		return render.PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500, FocusColor: render.RGB{R: 0xff}}, true
	}

	// Activate → takeover settings + switch fire once.
	c.publish(snap)
	if n := len(pub.SettingsSnapshot()); n != 1 {
		t.Fatalf("settings after activate = %d, want 1", n)
	}
	if n := len(pub.SwitchesSnapshot()); n != 1 {
		t.Fatalf("switches after activate = %d, want 1", n)
	}

	// A publish within the re-assert interval does NOT re-issue (edge already
	// applied; no need to churn the device).
	clk.Advance(pomoReassertInterval / 2)
	c.publish(snap)
	if n := len(pub.SettingsSnapshot()); n != 1 {
		t.Fatalf("settings within interval = %d, want 1 (no churn)", n)
	}
	if n := len(pub.SwitchesSnapshot()); n != 1 {
		t.Fatalf("switches within interval = %d, want 1", n)
	}

	// Past the interval, the takeover is re-asserted (Settings + Switch) so a
	// device reboot — which silently clears ATRANS/BLOCKN and the forced app
	// slot — recovers without the coordinator observing the reboot.
	clk.Advance(pomoReassertInterval)
	c.publish(snap)
	s := pub.SettingsSnapshot()
	if len(s) != 2 || s[1]["ATRANS"] != false || s[1]["BLOCKN"] != true {
		t.Fatalf("re-assert settings = %+v, want a 2nd ATRANS:false BLOCKN:true", s)
	}
	if n := len(pub.SwitchesSnapshot()); n != 2 {
		t.Fatalf("switches after re-assert = %d, want 2", n)
	}

	// Deactivate → single restore, flag cleared.
	c.pomoView = func() (render.PomodoroView, bool) { return render.PomodoroView{}, false }
	clk.Advance(time.Second)
	c.publish(snap)
	s = pub.SettingsSnapshot()
	if len(s) != 3 || s[2]["ATRANS"] != true || s[2]["BLOCKN"] != false {
		t.Fatalf("restore settings = %+v, want ATRANS:true BLOCKN:false", s)
	}
}

func TestCoordinatorRestoresTakeoverOnShutdown(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), nil)

	// Not in takeover → no restore call.
	c.restorePomoTakeoverOnExit()
	if n := len(pub.SettingsSnapshot()); n != 0 {
		t.Fatalf("restore without active takeover wrote %d settings, want 0", n)
	}

	// In takeover → restore ATRANS/BLOCKN once and clear the flag.
	c.pomoTakeover = true
	c.restorePomoTakeoverOnExit()
	s := pub.SettingsSnapshot()
	if len(s) != 1 || s[0]["ATRANS"] != true || s[0]["BLOCKN"] != false {
		t.Fatalf("shutdown restore settings = %+v, want ATRANS:true BLOCKN:false", s)
	}
	if c.pomoTakeover {
		t.Fatal("takeover flag should be cleared after restore")
	}
}

func TestCoordinatorHiddenAppDoesNotGrabAttentionLock(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), nil)
	c.ctx = context.Background()

	snap := Snapshot{Now: time.Now(), Sessions: []render.Session{
		{Source: "mbp", Tool: "codex", Session: "b", State: "waiting"},
	}}
	c.snapshot = func() Snapshot { return snap }
	c.hiddenApps = func() map[string]bool { return map[string]bool{"codex": true} }

	// codex (hidden) transitions into an attention state.
	c.onUpsert("mbp/codex/b", "running", "waiting")

	if c.locked {
		t.Fatalf("hidden codex grabbed the attention lock: lockedKey=%q", c.lockedKey)
	}
	if c.pointer == "mbp/codex/b" {
		t.Fatalf("hidden codex became the pointer: %q", c.pointer)
	}
}

func TestCoordinatorHidesAppFromDisplay(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), nil)
	c.ctx = context.Background()

	snap := Snapshot{Now: time.Now(), Sessions: []render.Session{
		{Source: "mbp", Tool: "claude", Session: "a", State: "running"},
		{Source: "mbp", Tool: "codex", Session: "b", State: "running"},
	}}
	c.snapshot = func() Snapshot { return snap }

	hidden := map[string]bool{"codex": true}
	c.hiddenApps = func() map[string]bool { return hidden }

	c.onTick()
	if c.pointer == "mbp/codex/b" {
		t.Fatalf("hidden codex session became the display pointer: %q", c.pointer)
	}

	// With codex hidden and claude present, the pointer must be the claude session.
	if c.pointer != "mbp/claude/a" {
		t.Fatalf("pointer = %q, want mbp/claude/a", c.pointer)
	}
}
