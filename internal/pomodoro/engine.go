package pomodoro

import (
	"sync"
	"time"
)

type Phase string

const (
	PhaseIdle  Phase = "idle"
	PhaseFocus Phase = "focus"
	PhaseShort Phase = "short_break"
	PhaseLong  Phase = "long_break"
)

type Settings struct {
	FocusMin         int
	ShortMin         int
	LongMin          int
	RoundsBeforeLong int
	AutoStartNext    bool
	MaxSessionMin    int
}

type Status struct {
	Phase        Phase `json:"phase"`
	Running      bool  `json:"running"`
	Paused       bool  `json:"paused"`
	RemainingSec int   `json:"remaining_sec"`
	PlannedSec   int   `json:"planned_sec"`
	Round        int   `json:"round"`
}

type PhaseResult struct {
	Phase      Phase
	PlannedSec int
	ActualSec  int
	Completed  bool
	Reason     string
}

type Clock interface{ Now() time.Time }

type Engine struct {
	mu       sync.Mutex
	settings Settings
	clk      Clock

	phase   Phase
	running bool
	paused  bool

	startedAt   time.Time
	accumPaused time.Duration
	pausedAt    time.Time

	focusCount int

	sessionStartedAt time.Time
}

func New(s Settings, clk Clock) *Engine {
	return &Engine{settings: s, clk: clk, phase: PhaseIdle}
}

func (e *Engine) UpdateSettings(s Settings) {
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
}

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

func (e *Engine) beginLocked(p Phase, now time.Time) {
	e.phase = p
	e.running = true
	e.paused = false
	e.startedAt = now
	e.accumPaused = 0
	e.pausedAt = time.Time{}
}

func (e *Engine) parkLocked(p Phase) {
	e.phase = p
	e.running = false
	e.paused = false
	e.accumPaused = 0
	e.pausedAt = time.Time{}
}

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

func (e *Engine) Start(p Phase) {
	e.mu.Lock()
	wasIdle := e.phase == PhaseIdle
	now := e.clk.Now()
	e.beginLocked(p, now)
	if wasIdle {
		e.sessionStartedAt = now
	}
	e.mu.Unlock()
}

func (e *Engine) Pause(now time.Time) {
	e.mu.Lock()
	if e.running && !e.paused {
		e.paused = true
		e.pausedAt = now
	}
	e.mu.Unlock()
}

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
	e.sessionStartedAt = time.Time{}
	return res
}

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

func (e *Engine) Tick(now time.Time) *PhaseResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running || e.paused || e.phase == PhaseIdle {
		return nil
	}
	if e.settings.MaxSessionMin > 0 && !e.sessionStartedAt.IsZero() &&
		now.Sub(e.sessionStartedAt) >= time.Duration(e.settings.MaxSessionMin)*time.Minute {
		res := e.endResultLocked(now, false, "max_session")
		e.phase = PhaseIdle
		e.running = false
		e.paused = false
		e.focusCount = 0
		e.sessionStartedAt = time.Time{}
		return res
	}
	planned := time.Duration(e.plannedSec(e.phase)) * time.Second
	if e.elapsedLocked(now) < planned {
		return nil
	}
	res := e.endResultLocked(now, true, "completed")
	res.ActualSec = res.PlannedSec
	next := e.advanceLocked(res.Phase)
	if e.settings.AutoStartNext {
		e.beginLocked(next, now)
	} else {
		e.parkLocked(next)
	}
	return res
}

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
	default:
		return PhaseFocus
	}
}

func (e *Engine) Status(now time.Time) Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(now)
}

func (e *Engine) statusLocked(now time.Time) Status {
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

func (e *Engine) Snapshot(now time.Time) (st Status, end time.Time, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st = e.statusLocked(now)
	if !e.running || e.paused || e.phase == PhaseIdle {
		return st, time.Time{}, false
	}
	return st, e.startedAt.Add(e.accumPaused + time.Duration(st.PlannedSec)*time.Second), true
}

func (e *Engine) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.phase != PhaseIdle
}
