package main

import (
	"errors"
	"testing"
	"time"
)

// makeAlarmCoord creates a coordinator wired with a usage store, fake clock,
// and recording publisher — the minimal setup for alarm tests. The coordinator
// goroutine is NOT started; tests drive checkLimitAlarms directly.
func makeAlarmCoord(t *testing.T) (*coordinator, *UsageStore, *recordingPublisher, *fakeClock) {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	pub := &recordingPublisher{}
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	c := newCoordinator(cfg, nil, pub, clk, testLogger(), nil)
	st := newUsageStore()
	c.usage = st
	c.snapshot = func() Snapshot { return Snapshot{} }
	return c, st, pub, clk
}

func TestLimitAlarmArmsFiresOnce(t *testing.T) {
	c, st, pub, clk := makeAlarmCoord(t)
	t0 := clk.Now()

	// Arm: claude at 100%, resets at T0+90.
	resetAt := t0.Unix() + 90
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 100, ResetsAt: resetAt},
		UpdatedAt: t0,
	})

	snap := Snapshot{}

	// T0: arm, no fire.
	c.checkLimitAlarms(t0, snap)
	if len(pub.NotifySnapshot()) != 0 {
		t.Fatalf("at T0: expected 0 notifies, got %d", len(pub.NotifySnapshot()))
	}
	if c.alarmArmed["claude"] != resetAt {
		t.Fatalf("alarm not armed at T0")
	}

	// T0+100: inside grace (armed+60 = T0+150) — still no fire.
	clk.Advance(100 * time.Second)
	c.checkLimitAlarms(clk.Now(), snap)
	if len(pub.NotifySnapshot()) != 0 {
		t.Fatalf("at T0+100: expected 0 notifies (inside grace), got %d", len(pub.NotifySnapshot()))
	}

	// At T0+160 (past armed+grace = T0+150) the store is still fresh and still
	// reports (100, T0+90); the drift guard doesn't apply (resetAt == armed,
	// not later), so the alarm fires.
	clk.Advance(60 * time.Second) // now T0+160
	c.checkLimitAlarms(clk.Now(), snap)
	notifies := pub.NotifySnapshot()
	if len(notifies) != 1 {
		t.Fatalf("at T0+160: expected exactly 1 notify, got %d", len(notifies))
	}
	if notifies[0]["text"] != "CLAUDE 5H RESET" {
		t.Fatalf("notify text = %v, want CLAUDE 5H RESET", notifies[0]["text"])
	}
	if notifies[0]["soundRtttl"] != limitAlarmRTTTL {
		t.Fatalf("alarm soundRtttl = %v, want %q", notifies[0]["soundRtttl"], limitAlarmRTTTL)
	}
	if notifies[0]["name"] != notifyNameUsageAlarm {
		t.Fatalf("alarm name = %v, want %q", notifies[0]["name"], notifyNameUsageAlarm)
	}
	if rtttls := pub.RTTTLsSnapshot(); len(rtttls) != 0 {
		t.Fatalf("the alarm chime rides on the notification, got %d out-of-band plays", len(rtttls))
	}

	// T0+170: fired dedupe — still 1 notify.
	clk.Advance(10 * time.Second)
	c.checkLimitAlarms(clk.Now(), snap)
	if len(pub.NotifySnapshot()) != 1 {
		t.Fatalf("at T0+170: expected still 1 notify (dedupe), got %d", len(pub.NotifySnapshot()))
	}
}

func TestLimitAlarmReArmsOnDriftedReset(t *testing.T) {
	c, st, pub, clk := makeAlarmCoord(t)
	t0 := clk.Now()

	// Arm at resetAt=T0+90.
	resetAt1 := t0.Unix() + 90
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 100, ResetsAt: resetAt1},
		UpdatedAt: t0,
	})

	snap := Snapshot{}
	c.checkLimitAlarms(t0, snap)

	// At T0+160 (past armed+grace), fresh data arrives with still >=99.5 but
	// a later resetAt=T0+400 — estimate drifted, re-arm, no fire.
	clk.Advance(160 * time.Second)
	resetAt2 := t0.Unix() + 400
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 100, ResetsAt: resetAt2},
		UpdatedAt: clk.Now(),
	})
	c.checkLimitAlarms(clk.Now(), snap)
	if len(pub.NotifySnapshot()) != 0 {
		t.Fatalf("after drift re-arm: expected 0 notifies, got %d", len(pub.NotifySnapshot()))
	}
	if c.alarmArmed["claude"] != resetAt2 {
		t.Fatalf("re-armed value wrong: got %d, want %d", c.alarmArmed["claude"], resetAt2)
	}

	// At T0+461 (past resetAt2+grace=T0+460), usage now at 10% (store fresh).
	clk.Advance(301 * time.Second) // T0+461
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 10, ResetsAt: resetAt2},
		UpdatedAt: clk.Now(),
	})
	c.checkLimitAlarms(clk.Now(), snap)
	if len(pub.NotifySnapshot()) != 1 {
		t.Fatalf("after re-arm fires: expected 1 notify, got %d", len(pub.NotifySnapshot()))
	}
}

func TestLimitAlarmFallsBackToSessionData(t *testing.T) {
	c, _, pub, clk := makeAlarmCoord(t)
	t0 := clk.Now()

	// No Put to the usage store for claude. Session carries RateWindowPct=100.
	resetAt := t0.Unix() + 90
	pct := 100
	snap := Snapshot{Sessions: []Session{
		{Source: "mbp", Tool: "claude", Session: "s1", State: "running",
			RateWindowPct: &pct, RateResetAt: resetAt, UpdatedAt: t0},
	}}

	c.checkLimitAlarms(t0, snap)
	if c.alarmArmed["claude"] != resetAt {
		t.Fatalf("alarm not armed from session data")
	}

	// Past armed+grace; session is gone (empty snap) -> fires.
	clk.Advance(160 * time.Second)
	c.checkLimitAlarms(clk.Now(), Snapshot{})
	if len(pub.NotifySnapshot()) != 1 {
		t.Fatalf("expected 1 notify after session gone, got %d", len(pub.NotifySnapshot()))
	}
}

func TestLimitAlarmRespectsToggle(t *testing.T) {
	c, st, pub, clk := makeAlarmCoord(t)
	// Disable the alarm.
	disabled := false
	cfg := *c.loadCfg()
	cfg.LimitAlarm = &disabled
	c.loadCfg = func() *Config { return &cfg }

	t0 := clk.Now()
	resetAt := t0.Unix() + 90
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 100, ResetsAt: resetAt},
		UpdatedAt: t0,
	})

	snap := Snapshot{}
	// Advance through the full timeline: arm check, grace, fire window.
	for _, d := range []time.Duration{0, 100 * time.Second, 60 * time.Second, 10 * time.Second} {
		clk.Advance(d)
		c.checkLimitAlarms(clk.Now(), snap)
	}

	if len(pub.NotifySnapshot()) != 0 {
		t.Fatalf("toggle off: expected 0 notifies, got %d", len(pub.NotifySnapshot()))
	}
	if len(c.alarmArmed) != 0 {
		t.Fatalf("toggle off: alarm should never arm, got %v", c.alarmArmed)
	}
}

func TestLimitAlarmNotifyFailureRetries(t *testing.T) {
	c, st, pub, clk := makeAlarmCoord(t)
	t0 := clk.Now()

	resetAt := t0.Unix() + 90
	st.Put("claude", ToolUsage{
		FiveHour:  &UsageWindow{UsedPercent: 100, ResetsAt: resetAt},
		UpdatedAt: t0,
	})

	// Arm.
	snap := Snapshot{}
	c.checkLimitAlarms(t0, snap)

	// First attempt at T0+160: Notify returns an error.
	callCount := 0
	pub.mu.Lock()
	pub.failNotify = func() error {
		callCount++
		if callCount == 1 {
			return errors.New("device unreachable")
		}
		return nil
	}
	pub.mu.Unlock()

	clk.Advance(160 * time.Second)
	c.checkLimitAlarms(clk.Now(), snap)
	// Still armed, no successful notify yet.
	if len(pub.NotifySnapshot()) != 0 {
		t.Fatalf("after first failure: expected 0 successful notifies, got %d", len(pub.NotifySnapshot()))
	}
	if _, stillArmed := c.alarmArmed["claude"]; !stillArmed {
		t.Fatal("after failure: alarm should remain armed for retry")
	}

	// Second attempt: succeeds.
	clk.Advance(5 * time.Second)
	c.checkLimitAlarms(clk.Now(), snap)
	if len(pub.NotifySnapshot()) != 1 {
		t.Fatalf("after retry: expected 1 notify, got %d", len(pub.NotifySnapshot()))
	}
	if _, stillArmed := c.alarmArmed["claude"]; stillArmed {
		t.Fatal("after success: alarm should be disarmed")
	}
}
