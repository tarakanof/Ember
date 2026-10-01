package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/render"
)

func holdFixture(t *testing.T, state string) (*coordinator, *recordingPublisher, *Snapshot, *bool) {
	t.Helper()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	c := newCoordinator(cfg, func() *Config { return &cfg }, pub, realClock{}, testLogger(), nil)
	c.ctx = context.Background()
	snap := &Snapshot{Now: time.Now(), Sessions: []render.Session{
		{Source: "mbp", Tool: "claude", Session: "a", State: state, UpdatedAt: time.Now()},
	}}
	c.snapshot = func() Snapshot { return *snap }
	pomo := false
	c.pomoView = func() (render.PomodoroView, bool) {
		if !pomo {
			return render.PomodoroView{}, false
		}
		return render.PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500, FocusColor: render.RGB{R: 0xff}}, true
	}
	return c, pub, snap, &pomo
}

type failingCustomAppPublisher struct {
	*recordingPublisher
	fail bool
}

func (p *failingCustomAppPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	if p.fail {
		return errUnreachableDevice
	}
	return p.recordingPublisher.CustomApp(ctx, name, payload)
}

var errUnreachableDevice = errors.New("device unreachable")

func TestCoordinatorRotatingFrameDoesNotSwitch(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "running")
	c.pointer = "mbp/claude/a"

	c.publish(*snap)
	c.publish(*snap)

	if sw := pub.SwitchesSnapshot(); len(sw) != 0 {
		t.Fatalf("switches for a rotating frame = %v, want none", sw)
	}
	if s := pub.SettingsSnapshot(); len(s) != 0 {
		t.Fatalf("settings for a rotating frame = %+v, want none", s)
	}
}

func TestCoordinatorAttentionHoldSwitchesOnceOnTheEdge(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	appName := c.loadCfg().AWTRIX.AppName

	c.pointer = "mbp/claude/a"
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 0 {
		t.Fatalf("switches before the lock = %v, want none", sw)
	}

	c.locked, c.lockedKey = true, "mbp/claude/a"
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 || sw[0] != appName {
		t.Fatalf("switches on the lock edge = %v, want [%s]", sw, appName)
	}
	if m := pub.SwitchModesSnapshot(); m[0] != awtrix.SwitchInstant {
		t.Fatalf("attention switch mode = %v, want SwitchInstant", m[0])
	}

	c.publish(*snap)
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 {
		t.Fatalf("switches while still locked = %d, want 1 (edge only)", len(sw))
	}

	if s := pub.SettingsSnapshot(); len(s) != 0 {
		t.Fatalf("settings during an attention hold = %+v, want none", s)
	}

	c.locked, c.lockedKey = false, ""
	c.publish(*snap)
	if s := pub.SettingsSnapshot(); len(s) != 0 {
		t.Fatalf("settings on hold release = %+v, want none", s)
	}
	c.locked, c.lockedKey = true, "mbp/claude/a"
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 2 {
		t.Fatalf("switches after re-locking = %d, want 2", len(sw))
	}
}

func TestCoordinatorHoldSwitchesAfterThePush(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	appName := c.loadCfg().AWTRIX.AppName
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"

	c.publish(*snap)

	ops := pub.OpsSnapshot()
	want := []string{"push " + appName, "switch " + appName}
	if !slices.Equal(ops, want) {
		t.Fatalf("device call order = %v, want %v", ops, want)
	}
}

func TestCoordinatorHoldNotAppliedWhenPushFails(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	fail := &failingCustomAppPublisher{recordingPublisher: pub, fail: true}
	c.publisher = fail
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"

	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 0 {
		t.Fatalf("switches after a failed push = %v, want none", sw)
	}

	fail.fail = false
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 {
		t.Fatalf("switches after the retry succeeded = %d, want 1", len(sw))
	}
}

func TestCoordinatorPomodoroOutranksAttentionHold(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "waiting")
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"

	*pomo = true
	c.publish(*snap)
	s := pub.SettingsSnapshot()
	if len(s) != 1 || s[0]["autoTransition"] != false || s[0]["blockNavigation"] != true {
		t.Fatalf("takeover settings = %+v, want one autoTransition:false blockNavigation:true", s)
	}
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 {
		t.Fatalf("switches on the pomodoro edge = %d, want 1", len(sw))
	}
	if m := pub.SwitchModesSnapshot(); m[0] != awtrix.SwitchAnimated {
		t.Fatalf("pomodoro switch mode = %v, want SwitchAnimated", m[0])
	}

	*pomo = false
	c.publish(*snap)
	s = pub.SettingsSnapshot()
	if len(s) != 2 || s[1]["autoTransition"] != true || s[1]["blockNavigation"] != false {
		t.Fatalf("restore settings = %+v, want a 2nd autoTransition:true blockNavigation:false", s)
	}
	if sw := pub.SwitchesSnapshot(); len(sw) != 2 {
		t.Fatalf("switches after pomodoro handed over to the attention hold = %d, want 2", len(sw))
	}
}

func TestCoordinatorRepublishReassertsAttentionHold(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"
	c.lockEnteredAt = time.Now()

	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 {
		t.Fatalf("switches on the lock edge = %d, want 1", len(sw))
	}

	c.onRepublish()
	if sw := pub.SwitchesSnapshot(); len(sw) != 2 {
		t.Fatalf("switches after republish = %d, want 2 (hold re-asserted)", len(sw))
	}
}

func TestCoordinatorIdleFrameDoesNotPin(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "running")
	snap.Sessions = nil

	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 0 {
		t.Fatalf("switches for the dimmed idle frame = %v, want none", sw)
	}
}

func TestCoordinatorReleasesHoldWhenNothingToPublish(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "running")
	snap.Sessions = nil
	c.idleSince = time.Now().Add(-time.Duration(c.loadCfg().Display.IdleRestoreSeconds+1) * time.Second)
	c.hold = holdPomodoro

	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 1 || s[0]["autoTransition"] != true || s[0]["blockNavigation"] != false {
		t.Fatalf("settings on release with nothing to publish = %+v, want one restore", s)
	}
	if c.hold != holdNone {
		t.Fatalf("hold after publishing nothing = %v, want holdNone", c.hold)
	}
}
