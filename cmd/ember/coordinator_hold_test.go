package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// holdFixture builds a coordinator whose snapshot holds one claude session in
// the given state, with the Pomodoro view wired to a caller-controlled flag.
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

// failingCustomAppPublisher fails CustomApp while fail is set; every other
// Publisher method delegates to the embedded recorder.
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

// A rotating (merely-running) frame must never pin the device: it shares the
// loop with the clock's own apps.
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

// The attention hold is a forced PUT /api/v1/apps/active, issued once on the
// lock edge and released without a device call.
func TestCoordinatorAttentionHoldSwitchesOnceOnTheEdge(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	appName := c.loadCfg().AWTRIX.AppName

	// Not locked yet: the waiting session still just rotates.
	c.pointer = "mbp/claude/a"
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 0 {
		t.Fatalf("switches before the lock = %v, want none", sw)
	}

	// Lock edge → exactly one switch to the ember app.
	c.locked, c.lockedKey = true, "mbp/claude/a"
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 || sw[0] != appName {
		t.Fatalf("switches on the lock edge = %v, want [%s]", sw, appName)
	}

	// Still locked → no per-tick switch spam.
	c.publish(*snap)
	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 {
		t.Fatalf("switches while still locked = %d, want 1 (edge only)", len(sw))
	}

	// An attention hold is short and self-expiring: it must not touch the
	// device's rotation or button-navigation settings at all.
	if s := pub.SettingsSnapshot(); len(s) != 0 {
		t.Fatalf("settings during an attention hold = %+v, want none", s)
	}

	// Release → nothing to undo device-side; a later re-lock switches again.
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

// The forced switch must follow the push that creates the app: NG answers 404
// for an app that is not in the loop, so switch-then-push would lose the pin
// on the first hold after a reboot.
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

// A failed push must not flip the hold state: the next tick has to retry the
// whole edge (push + switch), not just the switch.
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

// Pomodoro outranks the attention hold: while a timer runs the device stays
// under the full takeover (rotation + native nav off), and when the timer ends
// with the attention lock still up, the frame hold takes over.
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

	// Timer ends, attention lock survives → rotation restored, and the frame
	// hold re-pins the app.
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

// A reboot arrives as cmdRepublish: pushed apps are gone from RAM, so the hold
// has to be re-asserted even though Ember's own hold state never changed.
func TestCoordinatorRepublishReassertsAttentionHold(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "waiting")
	c.locked, c.lockedKey, c.pointer = true, "mbp/claude/a", "mbp/claude/a"
	c.lockEnteredAt = time.Now() // onRepublish runs a tick; keep the lock alive

	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 1 {
		t.Fatalf("switches on the lock edge = %d, want 1", len(sw))
	}

	c.onRepublish()
	if sw := pub.SwitchesSnapshot(); len(sw) != 2 {
		t.Fatalf("switches after republish = %d, want 2 (hold re-asserted)", len(sw))
	}
}

// The idle frames ask for a long dwell but nothing about them is urgent, so
// they must not shove the clock's own apps off the screen.
func TestCoordinatorIdleFrameDoesNotPin(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "running")
	snap.Sessions = nil

	c.publish(*snap)
	if sw := pub.SwitchesSnapshot(); len(sw) != 0 {
		t.Fatalf("switches for the dimmed idle frame = %v, want none", sw)
	}
}

// Reaching the "publish nothing at all" state must still release a Pomodoro
// takeover, or the clock stays frozen on a stale frame with rotation disabled.
func TestCoordinatorReleasesHoldWhenNothingToPublish(t *testing.T) {
	c, pub, snap, _ := holdFixture(t, "running")
	snap.Sessions = nil
	c.idleSince = time.Now().Add(-time.Duration(c.loadCfg().Display.IdleRestoreSeconds+1) * time.Second)
	c.hold = holdPomodoro // pretend a takeover was in force

	c.publish(*snap)

	s := pub.SettingsSnapshot()
	if len(s) != 1 || s[0]["autoTransition"] != true || s[0]["blockNavigation"] != false {
		t.Fatalf("settings on release with nothing to publish = %+v, want one restore", s)
	}
	if c.hold != holdNone {
		t.Fatalf("hold after publishing nothing = %v, want holdNone", c.hold)
	}
}
