package main

import (
	"slices"
	"testing"
	"time"
)

func alarmUsage(pct float64, resetAt int64) usageState {
	return usageState{Tools: map[string]usageToolState{
		"claude": {HaveFiveHour: true, FiveHourPct: pct, ResetAt: resetAt},
	}}
}

func TestLimitAlarmStateFiresOncePerReset(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	reset := t0.Unix() + 90
	var a limitAlarmState
	full := alarmUsage(100, reset)

	if due := a.due(full, t0); len(due) != 0 || a.alarmArmed["claude"] != reset {
		t.Fatalf("T0: due %v, armed %v", due, a.alarmArmed)
	}
	if due := a.due(full, t0.Add(149*time.Second)); len(due) != 0 {
		t.Fatalf("inside grace: due %v", due)
	}
	due := a.due(full, t0.Add(150*time.Second))
	if !slices.Equal(due, []limitAlarmFire{{Tool: "claude", ResetAt: reset}}) {
		t.Fatalf("after grace: due %v", due)
	}
	a.fired(due[0])
	if _, armed := a.alarmArmed["claude"]; armed || a.alarmFired["claude"] != reset {
		t.Fatalf("after fired: armed %v fired %v", a.alarmArmed, a.alarmFired)
	}
	if due := a.due(full, t0.Add(160*time.Second)); len(due) != 0 {
		t.Fatalf("after fired: due %v", due)
	}
}

func TestLimitAlarmStateUnconfirmedFireStaysDue(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	reset := t0.Unix() + 90
	var a limitAlarmState
	a.due(alarmUsage(100, reset), t0)

	for _, at := range []time.Duration{150 * time.Second, 155 * time.Second} {
		due := a.due(usageState{}, t0.Add(at))
		if !slices.Equal(due, []limitAlarmFire{{Tool: "claude", ResetAt: reset}}) {
			t.Fatalf("T0+%v: due %v, want claude until fired", at, due)
		}
	}
}

func TestLimitAlarmStateRearmsOnDrift(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	reset1, reset2 := t0.Unix()+90, t0.Unix()+3600
	var a limitAlarmState
	a.due(alarmUsage(100, reset1), t0)

	if due := a.due(alarmUsage(100, reset2), t0.Add(160*time.Second)); len(due) != 0 {
		t.Fatalf("drifted reset fired: %v", due)
	}
	if a.alarmArmed["claude"] != reset2 {
		t.Fatalf("armed %d, want %d", a.alarmArmed["claude"], reset2)
	}
}

func TestLimitAlarmStateArming(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name  string
		usage usageState
		armed bool
	}{
		{"at threshold", alarmUsage(limitAlarmThreshold, t0.Unix()+90), true},
		{"below threshold", alarmUsage(99.4, t0.Unix()+90), false},
		{"reset already passed", alarmUsage(100, t0.Unix()), false},
		{"no 5h window", usageState{Tools: map[string]usageToolState{"claude": {FiveHourPct: 100, ResetAt: t0.Unix() + 90}}}, false},
		{"untracked tool", usageState{Tools: map[string]usageToolState{"gemini": {HaveFiveHour: true, FiveHourPct: 100, ResetAt: t0.Unix() + 90}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a limitAlarmState
			a.due(tc.usage, t0)
			if got := len(a.alarmArmed) > 0; got != tc.armed {
				t.Fatalf("armed %v, want %v", a.alarmArmed, tc.armed)
			}
		})
	}
}

func TestLimitAlarmStateDisableDropsArmed(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	var a limitAlarmState
	a.due(alarmUsage(100, t0.Unix()+90), t0)
	a.disable()
	if due := a.due(usageState{}, t0.Add(time.Hour)); len(due) != 0 {
		t.Fatalf("disabled alarm fired after re-enable: %v", due)
	}
}

func TestLimitAlarmStateToolsFireInOrder(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	reset := t0.Unix() + 90
	both := usageState{Tools: map[string]usageToolState{
		"codex":  {HaveFiveHour: true, FiveHourPct: 100, ResetAt: reset},
		"claude": {HaveFiveHour: true, FiveHourPct: 100, ResetAt: reset},
	}}
	var a limitAlarmState
	a.due(both, t0)
	due := a.due(usageState{}, t0.Add(160*time.Second))
	want := []limitAlarmFire{{Tool: "claude", ResetAt: reset}, {Tool: "codex", ResetAt: reset}}
	if !slices.Equal(due, want) {
		t.Fatalf("due %v, want %v", due, want)
	}
}
