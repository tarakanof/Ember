package main

import (
	"testing"
	"time"
)

// indicatorTestNow is noon local: inside the indicator tests' clock, outside the
// default 22:00–08:00 quiet window.
var indicatorTestNow = time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)

// indicatorCoord builds a coordinator over a snapshot of sessions, with the
// indicator feature enabled unless off is set, and a clock at 12:00 local
// (outside the default quiet window).
func indicatorCoord(t *testing.T, on bool, sessions ...Session) (*coordinator, *recordingPublisher, *fakeClock) {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.Indicators = on
	pub := &recordingPublisher{}
	clk := &fakeClock{now: indicatorTestNow}
	c := newCoordinator(cfg, nil, pub, clk, testLogger(), nil)
	snap := Snapshot{Sessions: sessions}
	c.snapshot = func() Snapshot { return snap }
	return c, pub, clk
}

func TestIndicatorsOffByDefaultTouchNothing(t *testing.T) {
	c, pub, _ := indicatorCoord(t, false,
		Session{Source: "a", Tool: "b", Session: "s", State: "running", UpdatedAt: indicatorTestNow})
	c.publish(c.snapshot())
	if got := pub.IndicatorCallsSnapshot(); len(got) != 0 {
		t.Fatalf("display.indicators=false must write no indicators, got %v", got)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.clearedIndicators) != 0 {
		t.Fatalf("display.indicators=false must not clear indicators either, got %v", pub.clearedIndicators)
	}
}

func TestIndicatorRunningSessionLightsIndicatorOne(t *testing.T) {
	c, pub, _ := indicatorCoord(t, true,
		Session{Source: "a", Tool: "b", Session: "s", State: "running", UpdatedAt: indicatorTestNow})
	c.publish(c.snapshot())
	calls := pub.IndicatorCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("want exactly one indicator write, got %v", calls)
	}
	if calls[0].index != 1 {
		t.Errorf("index = %d, want 1", calls[0].index)
	}
	if calls[0].payload["color"] != indicatorRunningColor {
		t.Errorf("color = %v, want %q", calls[0].payload["color"], indicatorRunningColor)
	}
	if calls[0].payload["blinkMs"] != 0 {
		t.Errorf("blinkMs = %v, want 0 (steady)", calls[0].payload["blinkMs"])
	}
}

func TestIndicatorIdleSessionsLightNothing(t *testing.T) {
	c, pub, _ := indicatorCoord(t, true,
		Session{Source: "a", Tool: "b", Session: "s", State: "done", UpdatedAt: indicatorTestNow})
	c.publish(c.snapshot())
	if got := pub.IndicatorCallsSnapshot(); len(got) != 0 {
		t.Fatalf("a done session is not 'running', got %v", got)
	}
}

func TestIndicatorAttentionBlinksPerState(t *testing.T) {
	cases := []struct {
		state string
		color string
	}{
		{"waiting", indicatorWaitingColor},
		{"error", indicatorErrorColor},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			c, pub, _ := indicatorCoord(t, true,
				Session{Source: "a", Tool: "b", Session: "s", State: tc.state, UpdatedAt: indicatorTestNow})
			c.locked, c.lockedKey = true, "a/b/s"
			c.publish(c.snapshot())
			var found map[string]any
			for _, call := range pub.IndicatorCallsSnapshot() {
				if call.index == 2 {
					found = call.payload
				}
			}
			if found == nil {
				t.Fatalf("indicator 2 not written: %v", pub.IndicatorCallsSnapshot())
			}
			if found["color"] != tc.color {
				t.Errorf("color = %v, want %q", found["color"], tc.color)
			}
			if found["blinkMs"] != indicatorAttentionBlinkMs {
				t.Errorf("blinkMs = %v, want %d", found["blinkMs"], indicatorAttentionBlinkMs)
			}
		})
	}
}

func TestIndicatorAttentionOffWhenUnlocked(t *testing.T) {
	c, pub, _ := indicatorCoord(t, true,
		Session{Source: "a", Tool: "b", Session: "s", State: "waiting", UpdatedAt: indicatorTestNow})
	c.publish(c.snapshot())
	for _, call := range pub.IndicatorCallsSnapshot() {
		if call.index == 2 {
			t.Fatalf("indicator 2 must follow the attention lock, not a bare waiting session: %v", call.payload)
		}
	}
}

func TestIndicatorQuietHoursLightsIndicatorThree(t *testing.T) {
	c, pub, _ := indicatorCoord(t, true)
	cur := *c.loadCfg()
	cur.QuietHours.Enabled = true
	cur.QuietHours.Start = "11:00"
	cur.QuietHours.End = "13:00"
	c.loadCfg = func() *Config { return &cur }

	c.publish(c.snapshot())
	calls := pub.IndicatorCallsSnapshot()
	if len(calls) != 1 || calls[0].index != 3 {
		t.Fatalf("want one write to indicator 3, got %v", calls)
	}
	if calls[0].payload["color"] != indicatorQuietColor {
		t.Errorf("color = %v, want %q", calls[0].payload["color"], indicatorQuietColor)
	}
	if calls[0].payload["blinkMs"] != 0 {
		t.Errorf("blinkMs = %v, want 0 (steady)", calls[0].payload["blinkMs"])
	}
}

func TestIndicatorsWrittenOnlyOnChange(t *testing.T) {
	running := Session{Source: "a", Tool: "b", Session: "s", State: "running", UpdatedAt: time.Now()}
	c, pub, _ := indicatorCoord(t, true, running)

	// Three publishes of the same desired state: one write.
	c.publish(c.snapshot())
	c.publish(c.snapshot())
	c.publish(c.snapshot())
	if got := pub.IndicatorCallsSnapshot(); len(got) != 1 {
		t.Fatalf("steady state must write once, got %d writes", len(got))
	}

	// The session goes away: indicator 1 is cleared, once.
	empty := Snapshot{}
	c.snapshot = func() Snapshot { return empty }
	c.publish(empty)
	c.publish(empty)
	pub.mu.Lock()
	cleared := append([]int(nil), pub.clearedIndicators...)
	pub.mu.Unlock()
	if len(cleared) != 1 || cleared[0] != 1 {
		t.Fatalf("cleared indicators = %v, want [1] exactly once", cleared)
	}
}

func TestIndicatorsReassertAfterRepublish(t *testing.T) {
	c, pub, _ := indicatorCoord(t, true,
		Session{Source: "a", Tool: "b", Session: "s", State: "running", UpdatedAt: time.Now()})
	c.publish(c.snapshot())
	if got := len(pub.IndicatorCallsSnapshot()); got != 1 {
		t.Fatalf("first publish writes = %d, want 1", got)
	}
	// A reboot dropped whatever the device held: the next cycle re-asserts.
	c.onRepublish()
	if got := len(pub.IndicatorCallsSnapshot()); got != 2 {
		t.Fatalf("after republish writes = %d, want 2 (re-asserted)", got)
	}
}

func TestIndicatorFailedWriteRetriesNextPublish(t *testing.T) {
	c, pub, _ := indicatorCoord(t, true,
		Session{Source: "a", Tool: "b", Session: "s", State: "running", UpdatedAt: time.Now()})
	pub.mu.Lock()
	pub.indicatorErr = errFakeDeviceDown
	pub.mu.Unlock()
	c.publish(c.snapshot())

	pub.mu.Lock()
	pub.indicatorErr = nil
	pub.mu.Unlock()
	c.publish(c.snapshot())

	if got := len(pub.IndicatorCallsSnapshot()); got != 2 {
		t.Fatalf("a failed write must not be cached as applied; writes = %d, want 2", got)
	}
}
