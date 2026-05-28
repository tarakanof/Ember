// Package pomodoro implements a clock-injected Pomodoro timer state machine
// and its statistics store. The engine is pure (no I/O): it advances only when
// Tick is called with the current time, so it is fully deterministic in tests
// and drives the device display from the awtrix-ai-status coordinator.
package pomodoro

import (
	"sync"
	"time"
)

// Phase is the current timer phase.
type Phase string

const (
	PhaseIdle  Phase = "idle"
	PhaseFocus Phase = "focus"
	PhaseShort Phase = "short_break"
	PhaseLong  Phase = "long_break"
)

// Settings are the user-configurable durations and behaviour.
type Settings struct {
	FocusMin         int
	ShortMin         int
	LongMin          int
	RoundsBeforeLong int
	AutoStartNext    bool
}

// Status is a point-in-time snapshot of the engine, computed for a given now.
type Status struct {
	Phase        Phase `json:"phase"`
	Running      bool  `json:"running"`
	Paused       bool  `json:"paused"`
	RemainingSec int   `json:"remaining_sec"`
	PlannedSec   int   `json:"planned_sec"`
	Round        int   `json:"round"` // completed focus phases in the current cycle
}

// PhaseResult is emitted whenever a phase ends (completed, skipped, stopped).
// The coordinator records it in the stats store.
type PhaseResult struct {
	Phase      Phase
	PlannedSec int
	ActualSec  int
	Completed  bool
	Reason     string // "completed" | "skipped" | "stopped"
}

// Clock abstracts wall time so the engine is deterministic in tests.
type Clock interface{ Now() time.Time }

// Engine is the Pomodoro state machine. All methods are safe for concurrent
// use; callers pass the current time so behaviour stays clock-injected.
type Engine struct {
	mu       sync.Mutex
	settings Settings
	clk      Clock

	phase   Phase
	running bool
	paused  bool

	startedAt   time.Time     // when the current phase's countdown began
	accumPaused time.Duration // total paused time within the current phase
	pausedAt    time.Time     // when the current pause began (valid iff paused)

	focusCount int // completed focus phases since the last long break
}

// New constructs an idle engine with the given settings.
func New(s Settings, clk Clock) *Engine {
	return &Engine{settings: s, clk: clk, phase: PhaseIdle}
}

// UpdateSettings replaces the settings. In-flight phase durations are not
// retroactively changed; new durations apply to subsequent phases.
func (e *Engine) UpdateSettings(s Settings) {
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
}

// CurrentSettings returns the active settings.
func (e *Engine) CurrentSettings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

func (e *Engine) plannedSec(p Phase) int {
	switch p {
	case PhaseFocus:
		return e.settings.FocusMin * 60
	case PhaseShort:
		return e.settings.ShortMin * 60
	case PhaseLong:
		return e.settings.LongMin * 60
	default:
		return 0
	}
}

// beginLocked starts phase p running from a full duration at now.
func (e *Engine) beginLocked(p Phase, now time.Time) {
	e.phase = p
	e.running = true
	e.paused = false
	e.startedAt = now
	e.accumPaused = 0
	e.pausedAt = time.Time{}
}

// parkLocked sets phase p as the pending next phase, not running.
func (e *Engine) parkLocked(p Phase) {
	e.phase = p
	e.running = false
	e.paused = false
	e.accumPaused = 0
	e.pausedAt = time.Time{}
}

// elapsedLocked returns how much of the current phase has elapsed at now,
// excluding paused time.
func (e *Engine) elapsedLocked(now time.Time) time.Duration {
	if e.phase == PhaseIdle {
		return 0
	}
	ref := now
	if e.paused {
		ref = e.pausedAt
	}
	d := ref.Sub(e.startedAt) - e.accumPaused
	if d < 0 {
		return 0
	}
	return d
}

// Start begins phase p (typically PhaseFocus) running from a full duration.
func (e *Engine) Start(p Phase) {
	e.mu.Lock()
	e.beginLocked(p, e.clk.Now())
	e.mu.Unlock()
}

// Pause freezes the countdown. No-op unless running and not already paused.
func (e *Engine) Pause(now time.Time) {
	e.mu.Lock()
	if e.running && !e.paused {
		e.paused = true
		e.pausedAt = now
	}
	e.mu.Unlock()
}

// Resume continues a paused phase, or starts a parked (pending) phase.
func (e *Engine) Resume(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.paused:
		e.accumPaused += now.Sub(e.pausedAt)
		e.paused = false
		e.pausedAt = time.Time{}
	case !e.running && e.phase != PhaseIdle:
		e.beginLocked(e.phase, now)
	}
}

// Stop ends the current phase early and returns to idle. Returns a not-completed
// PhaseResult, or nil if already idle.
func (e *Engine) Stop(now time.Time) *PhaseResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase == PhaseIdle {
		return nil
	}
	res := e.endResultLocked(now, false, "stopped")
	e.phase = PhaseIdle
	e.running = false
	e.paused = false
	e.focusCount = 0
	return res
}

// Skip ends the current phase (not completed) and advances to the next phase,
// auto-starting it. Returns the skipped PhaseResult, or nil if idle.
func (e *Engine) Skip(now time.Time) *PhaseResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase == PhaseIdle {
		return nil
	}
	res := e.endResultLocked(now, false, "skipped")
	next := e.advanceLocked(res.Phase)
	e.beginLocked(next, now)
	return res
}

// Tick advances the engine to now. If the running phase has elapsed it ends as
// completed, transitions to the next phase (auto-started or parked per
// settings), and returns the completed PhaseResult. Otherwise returns nil.
func (e *Engine) Tick(now time.Time) *PhaseResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running || e.paused || e.phase == PhaseIdle {
		return nil
	}
	planned := time.Duration(e.plannedSec(e.phase)) * time.Second
	if e.elapsedLocked(now) < planned {
		return nil
	}
	res := e.endResultLocked(now, true, "completed")
	res.ActualSec = res.PlannedSec // completed phases report their full planned length
	next := e.advanceLocked(res.Phase)
	if e.settings.AutoStartNext {
		e.beginLocked(next, now)
	} else {
		e.parkLocked(next)
	}
	return res
}

// endResultLocked builds a PhaseResult for the current phase at now.
func (e *Engine) endResultLocked(now time.Time, completed bool, reason string) *PhaseResult {
	planned := e.plannedSec(e.phase)
	actual := int(e.elapsedLocked(now) / time.Second)
	if actual > planned {
		actual = planned
	}
	return &PhaseResult{
		Phase:      e.phase,
		PlannedSec: planned,
		ActualSec:  actual,
		Completed:  completed,
		Reason:     reason,
	}
}

// advanceLocked computes the next phase after `ended` finished, updating the
// focus/long-break round counter. It does not start the next phase.
func (e *Engine) advanceLocked(ended Phase) Phase {
	switch ended {
	case PhaseFocus:
		e.focusCount++
		if e.settings.RoundsBeforeLong > 0 && e.focusCount >= e.settings.RoundsBeforeLong {
			return PhaseLong
		}
		return PhaseShort
	case PhaseLong:
		e.focusCount = 0
		return PhaseFocus
	default: // short break or anything else → back to focus
		return PhaseFocus
	}
}

// Status returns a snapshot computed at now.
func (e *Engine) Status(now time.Time) Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	planned := e.plannedSec(e.phase)
	remaining := planned
	if e.running {
		remaining = planned - int(e.elapsedLocked(now)/time.Second)
		if remaining < 0 {
			remaining = 0
		}
	}
	return Status{
		Phase:        e.phase,
		Running:      e.running,
		Paused:       e.paused,
		RemainingSec: remaining,
		PlannedSec:   planned,
		Round:        e.focusCount,
	}
}

// Active reports whether the engine currently owns the display (any non-idle
// phase, running or parked).
func (e *Engine) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.phase != PhaseIdle
}
