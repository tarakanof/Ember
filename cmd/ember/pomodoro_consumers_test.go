package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

func TestPomodoroConsumersAgree(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	at := t0.Add(10 * time.Minute)
	cases := []struct {
		name      string
		off       bool
		act       func(t *testing.T, e *pomodoro.Engine)
		knob      bool
		clock     bool
		busy      bool
		phase     pomodoro.Phase
		paused    bool
		counting  bool
		remaining int
	}{
		{"feature off", true, func(_ *testing.T, e *pomodoro.Engine) { e.Start(pomodoro.PhaseFocus) }, false, false, false, "", false, false, 0},
		{"idle", false, func(*testing.T, *pomodoro.Engine) {}, true, false, false, pomodoro.PhaseIdle, false, false, 0},
		{"focus running", false, func(_ *testing.T, e *pomodoro.Engine) { e.Start(pomodoro.PhaseFocus) }, true, true, true, pomodoro.PhaseFocus, false, true, 15 * 60},
		{"focus paused", false, func(_ *testing.T, e *pomodoro.Engine) {
			e.Start(pomodoro.PhaseFocus)
			e.Pause(t0.Add(5 * time.Minute))
		}, true, true, true, pomodoro.PhaseFocus, true, false, 20 * 60},
		{"short break after skip", false, func(_ *testing.T, e *pomodoro.Engine) {
			e.Start(pomodoro.PhaseFocus)
			e.Skip(t0.Add(8 * time.Minute))
		}, true, true, true, pomodoro.PhaseShort, false, true, 3 * 60},
		{"parked after a completed focus", false, func(t *testing.T, e *pomodoro.Engine) {
			e.Start(pomodoro.PhaseFocus)
			if res := e.Tick(t0.Add(25 * time.Minute)); res == nil || !res.Completed {
				t.Fatalf("tick = %+v, want the focus completed", res)
			}
		}, true, true, false, pomodoro.PhaseShort, false, false, 5 * 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newPomodoroApp(t)
			clk := &stepClock{now: t0}
			app.EnablePomodoro(pomodoro.New(pomodoro.Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4}, clk), app.store)
			tc.act(t, app.engine)
			if tc.off {
				app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false })
			}
			knob := app.knobPomo(at)
			clock, on := app.pomoView(at)
			busy := app.pomodoroBusy(at)
			if (knob != nil) != tc.knob || on != tc.clock || busy != tc.busy {
				t.Fatalf("knob %+v, clock on %v, busy %v; want knob %v, clock %v, busy %v",
					knob, on, busy, tc.knob, tc.clock, tc.busy)
			}
			if knob == nil {
				return
			}
			if knob.Phase != string(tc.phase) || knob.Paused != tc.paused || (knob.EndsAt != nil) != tc.counting || (knob.RemainingSec != nil) == tc.counting {
				t.Fatalf("knob = %+v", knob)
			}
			left := 0
			if tc.counting {
				left = int(*knob.EndsAt - at.Unix())
			} else {
				left = *knob.RemainingSec
			}
			if left != tc.remaining {
				t.Errorf("knob has %d s left, want %d", left, tc.remaining)
			}
			if !on {
				return
			}
			if clock.Phase != knob.Phase || clock.Paused != knob.Paused || clock.PlannedSec != knob.PlannedSec || clock.RemainingSec != tc.remaining {
				t.Errorf("clock %+v disagrees with knob %+v (want %d s left)", clock, knob, tc.remaining)
			}
		})
	}
}
