package pomodoro

import (
	"testing"
	"time"
)

// fakeClock is a manually-advanced clock for deterministic engine tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newTestEngine(clk *fakeClock) *Engine {
	return New(Settings{
		FocusMin:         25,
		ShortMin:         5,
		LongMin:          15,
		RoundsBeforeLong: 4,
		AutoStartNext:    false,
	}, clk)
}

func TestStartFocusSetsRunningStatus(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)

	e.Start(PhaseFocus)
	st := e.Status(clk.Now())

	if st.Phase != PhaseFocus {
		t.Fatalf("phase = %q, want focus", st.Phase)
	}
	if !st.Running || st.Paused {
		t.Fatalf("running=%v paused=%v, want running && !paused", st.Running, st.Paused)
	}
	if st.RemainingSec != 25*60 {
		t.Fatalf("remaining = %d, want %d", st.RemainingSec, 25*60)
	}
	if st.PlannedSec != 25*60 {
		t.Fatalf("planned = %d, want %d", st.PlannedSec, 25*60)
	}
}

func TestTickCountsDownRemaining(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)
	e.Start(PhaseFocus)

	clk.advance(60 * time.Second)
	if ended := e.Tick(clk.Now()); ended != nil {
		t.Fatalf("phase should not have ended after 60s, got %+v", ended)
	}
	if got := e.Status(clk.Now()).RemainingSec; got != 24*60 {
		t.Fatalf("remaining = %d, want %d", got, 24*60)
	}
}

func TestFocusCompletionParksWhenAutoStartOff(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)
	e.Start(PhaseFocus)

	clk.advance(25 * time.Minute)
	ended := e.Tick(clk.Now())
	if ended == nil {
		t.Fatal("expected PhaseResult on completion, got nil")
	}
	if !ended.Completed || ended.Reason != "completed" || ended.Phase != PhaseFocus {
		t.Fatalf("ended = %+v, want completed focus", ended)
	}
	st := e.Status(clk.Now())
	if st.Phase != PhaseShort {
		t.Fatalf("next phase = %q, want short_break", st.Phase)
	}
	if st.Running {
		t.Fatal("auto-start off: next phase must be parked (not running)")
	}
	if st.RemainingSec != 5*60 {
		t.Fatalf("parked remaining = %d, want %d", st.RemainingSec, 5*60)
	}
}

func TestFocusCompletionAutoStartsNext(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := New(Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4, AutoStartNext: true}, clk)
	e.Start(PhaseFocus)

	clk.advance(25 * time.Minute)
	if ended := e.Tick(clk.Now()); ended == nil {
		t.Fatal("expected completion")
	}
	st := e.Status(clk.Now())
	if st.Phase != PhaseShort || !st.Running {
		t.Fatalf("auto-start: got phase=%q running=%v, want short_break running", st.Phase, st.Running)
	}
}

func TestPauseFreezesAndResumeContinues(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)
	e.Start(PhaseFocus)

	clk.advance(60 * time.Second)
	e.Pause(clk.Now())
	if !e.Status(clk.Now()).Paused {
		t.Fatal("expected paused")
	}
	// Time passes while paused — remaining must not move.
	clk.advance(10 * time.Minute)
	if got := e.Status(clk.Now()).RemainingSec; got != 24*60 {
		t.Fatalf("paused remaining = %d, want %d (frozen)", got, 24*60)
	}
	// Ticking while paused never ends the phase.
	if ended := e.Tick(clk.Now()); ended != nil {
		t.Fatalf("paused tick must not end phase, got %+v", ended)
	}
	e.Resume(clk.Now())
	clk.advance(60 * time.Second)
	if got := e.Status(clk.Now()).RemainingSec; got != 23*60 {
		t.Fatalf("after resume remaining = %d, want %d", got, 23*60)
	}
}

func TestFourthBreakIsLongAndRoundResets(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := New(Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4, AutoStartNext: true}, clk)

	completeFocus := func() Phase {
		e.Start(PhaseFocus)
		clk.advance(25 * time.Minute)
		e.Tick(clk.Now())
		return e.Status(clk.Now()).Phase
	}
	completeBreak := func() {
		clk.advance(time.Duration(e.Status(clk.Now()).PlannedSec) * time.Second)
		e.Tick(clk.Now())
	}

	if p := completeFocus(); p != PhaseShort {
		t.Fatalf("break 1 = %q, want short", p)
	}
	completeBreak()
	if p := completeFocus(); p != PhaseShort {
		t.Fatalf("break 2 = %q, want short", p)
	}
	completeBreak()
	if p := completeFocus(); p != PhaseShort {
		t.Fatalf("break 3 = %q, want short", p)
	}
	completeBreak()
	if p := completeFocus(); p != PhaseLong {
		t.Fatalf("break 4 = %q, want long", p)
	}
	if r := e.Status(clk.Now()).Round; r != 4 {
		t.Fatalf("round at long break = %d, want 4", r)
	}
	// After the long break completes, the cycle resets to a fresh focus.
	completeBreak()
	st := e.Status(clk.Now())
	if st.Phase != PhaseFocus {
		t.Fatalf("after long break phase = %q, want focus", st.Phase)
	}
	if st.Round != 0 {
		t.Fatalf("round after long break = %d, want 0", st.Round)
	}
}

func TestSkipEndsPhaseNotCompleted(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)
	e.Start(PhaseFocus)
	clk.advance(3 * time.Minute)

	ended := e.Skip(clk.Now())
	if ended == nil || ended.Completed || ended.Reason != "skipped" {
		t.Fatalf("skip ended = %+v, want not-completed skipped", ended)
	}
	if ended.ActualSec != 3*60 {
		t.Fatalf("skip actual = %d, want 180", ended.ActualSec)
	}
	if e.Status(clk.Now()).Phase != PhaseShort {
		t.Fatalf("after skip phase = %q, want short_break", e.Status(clk.Now()).Phase)
	}
}

func TestStopReturnsToIdle(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)
	e.Start(PhaseFocus)
	clk.advance(2 * time.Minute)

	ended := e.Stop(clk.Now())
	if ended == nil || ended.Completed || ended.Reason != "stopped" {
		t.Fatalf("stop ended = %+v, want not-completed stopped", ended)
	}
	st := e.Status(clk.Now())
	if st.Phase != PhaseIdle || st.Running {
		t.Fatalf("after stop = %+v, want idle not-running", st)
	}
}

func TestResumeStartsParkedPhase(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk) // AutoStartNext false
	e.Start(PhaseFocus)
	clk.advance(25 * time.Minute)
	e.Tick(clk.Now()) // parks at short_break, not running

	e.Resume(clk.Now()) // resume should start the parked break
	st := e.Status(clk.Now())
	if st.Phase != PhaseShort || !st.Running || st.Paused {
		t.Fatalf("resume parked = %+v, want short_break running", st)
	}
	if st.RemainingSec != 5*60 {
		t.Fatalf("resumed parked remaining = %d, want %d", st.RemainingSec, 5*60)
	}
}

func TestStopWhenIdleReturnsNil(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := newTestEngine(clk)
	if ended := e.Stop(clk.Now()); ended != nil {
		t.Fatalf("stop while idle = %+v, want nil", ended)
	}
}
