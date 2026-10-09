package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

func TestPomodoroConsumersAgree(t *testing.T) {
	cases := []struct {
		name      string
		off       bool
		act       func(e *pomodoro.Engine)
		knob      bool
		clock     bool
		busy      bool
		phase     pomodoro.Phase
		paused    bool
		counting  bool
		remaining int
	}{
		{"feature off", true, func(e *pomodoro.Engine) { e.Start(pomodoro.PhaseFocus) }, false, false, false, "", false, false, 0},
		{"idle", false, func(*pomodoro.Engine) {}, true, false, false, pomodoro.PhaseIdle, false, false, 0},
		{"focus running", false, func(e *pomodoro.Engine) { e.Start(pomodoro.PhaseFocus) }, true, true, true, pomodoro.PhaseFocus, false, true, 25 * 60},
		{"focus paused", false, func(e *pomodoro.Engine) {
			e.Start(pomodoro.PhaseFocus)
			e.Pause(time.Now())
		}, true, true, true, pomodoro.PhaseFocus, true, false, 25 * 60},
		{"short break after skip", false, func(e *pomodoro.Engine) {
			e.Start(pomodoro.PhaseFocus)
			e.Skip(time.Now())
		}, true, true, true, pomodoro.PhaseShort, false, true, 5 * 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newPomodoroApp(t)
			tc.act(app.engine)
			if tc.off {
				app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false })
			}
			now := time.Now()
			knob := app.knobPomo(now)
			clock, on := app.pomoView()
			if (knob != nil) != tc.knob || on != tc.clock || app.pomodoroBusy() != tc.busy {
				t.Fatalf("knob %+v, clock on %v, busy %v; want knob %v, clock %v, busy %v",
					knob, on, app.pomodoroBusy(), tc.knob, tc.clock, tc.busy)
			}
			if knob == nil {
				return
			}
			if knob.Phase != string(tc.phase) || knob.Paused != tc.paused || (knob.EndsAt != nil) != tc.counting || (knob.RemainingSec != nil) == tc.counting {
				t.Errorf("knob = %+v", knob)
			}
			if tc.counting {
				if left := *knob.EndsAt - now.Unix(); left < int64(tc.remaining-1) || left > int64(tc.remaining) {
					t.Errorf("knob ends in %d s, want about %d", left, tc.remaining)
				}
			} else if *knob.RemainingSec != tc.remaining && tc.phase != pomodoro.PhaseIdle {
				t.Errorf("knob remaining = %d, want %d", *knob.RemainingSec, tc.remaining)
			}
			if !on {
				return
			}
			if clock.Phase != knob.Phase || clock.Paused != knob.Paused || clock.PlannedSec != knob.PlannedSec {
				t.Errorf("clock %+v disagrees with knob %+v", clock, knob)
			}
			if clock.RemainingSec < tc.remaining-1 || clock.RemainingSec > tc.remaining {
				t.Errorf("clock remaining = %d, want about %d", clock.RemainingSec, tc.remaining)
			}
		})
	}
}
