package main

import (
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

type pomodoroState struct {
	On       bool
	Status   pomodoro.Status
	Counting bool
	EndsAt   time.Time
}

func (a *App) pomodoroState(now time.Time) pomodoroState {
	if !a.pomodoroOn() {
		return pomodoroState{}
	}
	st, end, counting := a.engine.Snapshot(now)
	return pomodoroState{On: true, Status: st, Counting: counting, EndsAt: end}
}

func (s pomodoroState) active() bool { return s.On && s.Status.Phase != pomodoro.PhaseIdle }

func (s pomodoroState) busy() bool { return s.On && (s.Status.Running || s.Status.Paused) }
