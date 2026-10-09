package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

func TestPomodoroStateFollowsTheEngine(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		act      func(e *pomodoro.Engine, clk *stepClock)
		at       time.Duration
		phase    pomodoro.Phase
		active   bool
		busy     bool
		counting bool
		ends     time.Duration
		left     int
	}{
		{"idle", func(*pomodoro.Engine, *stepClock) {}, 0, pomodoro.PhaseIdle, false, false, false, 0, 0},
		{"counting", func(e *pomodoro.Engine, _ *stepClock) { e.Start(pomodoro.PhaseFocus) }, 10 * time.Minute,
			pomodoro.PhaseFocus, true, true, true, 25 * time.Minute, 15 * 60},
		{"paused", func(e *pomodoro.Engine, clk *stepClock) {
			e.Start(pomodoro.PhaseFocus)
			clk.now = t0.Add(5 * time.Minute)
			e.Pause(clk.now)
		}, 10 * time.Minute, pomodoro.PhaseFocus, true, true, false, 0, 20 * 60},
		{"overrun", func(e *pomodoro.Engine, _ *stepClock) { e.Start(pomodoro.PhaseShort) }, time.Hour,
			pomodoro.PhaseShort, true, true, true, 5 * time.Minute, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newPomodoroApp(t)
			clk := &stepClock{now: t0}
			app.EnablePomodoro(pomodoro.New(pomodoro.Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4}, clk), app.store)
			tc.act(app.engine, clk)
			s := app.pomodoroState(t0.Add(tc.at))
			if !s.On || s.Status.Phase != tc.phase || s.active() != tc.active || s.busy() != tc.busy || s.Counting != tc.counting {
				t.Fatalf("state = %+v active %v busy %v", s, s.active(), s.busy())
			}
			if s.Status.RemainingSec != tc.left {
				t.Errorf("remaining = %d, want %d", s.Status.RemainingSec, tc.left)
			}
			if tc.counting && !s.EndsAt.Equal(t0.Add(tc.ends)) {
				t.Errorf("ends at %v, want %v", s.EndsAt, t0.Add(tc.ends))
			}
			if !tc.counting && !s.EndsAt.IsZero() {
				t.Errorf("ends at %v while not counting", s.EndsAt)
			}
		})
	}
}

func TestPomodoroStateOffWithoutEngineOrFeature(t *testing.T) {
	app := newPomodoroApp(t)
	app.engine.Start(pomodoro.PhaseFocus)
	app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false })
	if s := app.pomodoroState(time.Now()); s.On || s.active() || s.busy() {
		t.Fatalf("feature off: %+v", s)
	}
	bare := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if s := bare.pomodoroState(time.Now()); s.On {
		t.Fatalf("no engine: %+v", s)
	}
}
