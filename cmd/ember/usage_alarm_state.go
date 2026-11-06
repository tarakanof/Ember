package main

import "time"

type limitAlarmFire struct {
	Tool    string
	ResetAt int64
}

type limitAlarmState struct {
	alarmArmed map[string]int64
	alarmFired map[string]int64
}

func (a *limitAlarmState) disable() { a.alarmArmed = nil }

func (a *limitAlarmState) due(state usageState, now time.Time) []limitAlarmFire {
	if a.alarmArmed == nil {
		a.alarmArmed = map[string]int64{}
		a.alarmFired = map[string]int64{}
	}
	var due []limitAlarmFire
	for _, tool := range usageTools {
		t := state.Tools[tool]
		pct, resetAt, ok := t.FiveHourPct, t.ResetAt, t.HaveFiveHour
		if ok && pct >= limitAlarmThreshold && resetAt > now.Unix() && a.alarmFired[tool] != resetAt {
			a.alarmArmed[tool] = resetAt
		}
		armed, isArmed := a.alarmArmed[tool]
		if !isArmed || now.Unix() < armed+limitAlarmGraceSec {
			continue
		}
		if ok && pct >= limitAlarmThreshold && resetAt > armed {
			a.alarmArmed[tool] = resetAt
			continue
		}
		due = append(due, limitAlarmFire{Tool: tool, ResetAt: armed})
	}
	return due
}

func (a *limitAlarmState) fired(f limitAlarmFire) {
	a.alarmFired[f.Tool] = f.ResetAt
	delete(a.alarmArmed, f.Tool)
}
