package main

import (
	"errors"
	"testing"
)

func TestPomoMenuTitle(t *testing.T) {
	cases := []struct {
		name string
		st   pomoState
		err  error
		want string
	}{
		{"error", pomoState{}, errors.New("x"), "Pomodoro: off"},
		{"idle", pomoState{Phase: "idle"}, nil, "Pomodoro: idle"},
		{"running", pomoState{Phase: "focus", Running: true, RemainingSec: 1490}, nil, "Pomodoro: focus 24:50 ▶"},
		{"paused", pomoState{Phase: "short_break", Running: true, Paused: true, RemainingSec: 65}, nil, "Pomodoro: break 01:05 ⏸"},
		{"parked", pomoState{Phase: "long_break", Running: false, RemainingSec: 900}, nil, "Pomodoro: long break 15:00 ▷"},
	}
	for _, tc := range cases {
		if got := pomoMenuTitle(tc.st, tc.err); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestPomoStatsTitle(t *testing.T) {
	if got := pomoStatsTitle(pomoStats{}, errors.New("x")); got != "Today: —" {
		t.Errorf("error case = %q", got)
	}
	got := pomoStatsTitle(pomoStats{Today: pomoDayStat{CompletedFocus: 4, FocusMin: 100}, Streak: 3}, nil)
	if got != "Today: 4 🍅 · 100 min · streak 3" {
		t.Errorf("got %q", got)
	}
}

func TestBuildPomoConfig(t *testing.T) {
	cfg, err := buildPomoConfig("30", "5", "15", "4", true, false, "#FF3B30", "#2EE85E", "")
	if err != nil {
		t.Fatalf("valid build error: %v", err)
	}
	if cfg.FocusMinutes != 30 || cfg.RoundsBeforeLongBreak != 4 || !cfg.AutoStartNext || cfg.Sound {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := buildPomoConfig("x", "5", "15", "4", false, true, "#FF3B30", "#2EE85E", ""); err == nil {
		t.Fatal("expected error for non-numeric focus")
	}
	if _, err := buildPomoConfig("999", "5", "15", "4", false, true, "#FF3B30", "#2EE85E", ""); err == nil {
		t.Fatal("expected range error for focus=999")
	}
	if _, err := buildPomoConfig("25", "5", "15", "4", false, true, "red", "#2EE85E", ""); err == nil {
		t.Fatal("expected error for bad focus colour")
	}
}

func TestPomoToggleVerb(t *testing.T) {
	cases := []struct {
		name string
		st   pomoState
		want string
	}{
		{"running", pomoState{Phase: "focus", Running: true}, "pause"},
		{"paused", pomoState{Phase: "focus", Running: true, Paused: true}, "resume"},
		{"parked", pomoState{Phase: "short_break", Running: false}, "resume"},
		{"idle", pomoState{Phase: "idle"}, "resume"},
	}
	for _, tc := range cases {
		if got := pomoToggleVerb(tc.st); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
