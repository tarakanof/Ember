package main

import (
	"fmt"
	"strconv"
	"strings"
)

// buildPomoConfig parses and validates the Pomodoro settings form fields into a
// pomoConfig, returning a user-facing error string on the first invalid field.
func buildPomoConfig(focus, short, long, rounds string, autoStart, sound bool, focusColor, breakColor, melody string) (pomoConfig, error) {
	parseRange := func(name, s string, lo, hi int) (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", name)
		}
		if n < lo || n > hi {
			return 0, fmt.Errorf("%s must be between %d and %d", name, lo, hi)
		}
		return n, nil
	}
	fm, err := parseRange("Focus", focus, 1, 180)
	if err != nil {
		return pomoConfig{}, err
	}
	sm, err := parseRange("Short break", short, 1, 60)
	if err != nil {
		return pomoConfig{}, err
	}
	lm, err := parseRange("Long break", long, 1, 180)
	if err != nil {
		return pomoConfig{}, err
	}
	rb, err := parseRange("Rounds", rounds, 1, 12)
	if err != nil {
		return pomoConfig{}, err
	}
	if !hexColorRe.MatchString(focusColor) {
		return pomoConfig{}, fmt.Errorf("Focus colour must be #RRGGBB")
	}
	if !hexColorRe.MatchString(breakColor) {
		return pomoConfig{}, fmt.Errorf("Break colour must be #RRGGBB")
	}
	return pomoConfig{
		FocusMinutes:          fm,
		ShortBreakMinutes:     sm,
		LongBreakMinutes:      lm,
		RoundsBeforeLongBreak: rb,
		AutoStartNext:         autoStart,
		Sound:                 sound,
		SoundMelody:           melody,
		FocusColor:            focusColor,
		BreakColor:            breakColor,
	}, nil
}

// pomoClientFromEnv builds a Pomodoro API client using the server URL + token
// stored in producer.env.
func pomoClientFromEnv(envPath string) *pomodoroClient {
	rec, _ := readEnv(envPath)
	if rec == nil {
		rec = &envRec{}
	}
	return newPomodoroClient(rec.get("STATUS_SERVER_URL"), rec.get("STATUS_TOKEN"))
}

// pomoMenuTitle formats the Pomodoro status line shown (disabled) in the menu.
// A fetch error (feature disabled / server down) reads as "off".
func pomoMenuTitle(st pomoState, err error) string {
	if err != nil {
		return "Pomodoro: off"
	}
	if st.Phase == "idle" || st.Phase == "" {
		return "Pomodoro: idle"
	}
	mm := st.RemainingSec / 60
	ss := st.RemainingSec % 60
	marker := "▶"
	if st.Paused {
		marker = "⏸"
	} else if !st.Running {
		marker = "▷" // parked (awaiting start)
	}
	return fmt.Sprintf("Pomodoro: %s %02d:%02d %s", phaseLabel(st.Phase), mm, ss, marker)
}

// phaseLabel maps an engine phase to a short human label.
func phaseLabel(phase string) string {
	switch phase {
	case "focus":
		return "focus"
	case "short_break":
		return "break"
	case "long_break":
		return "long break"
	default:
		return phase
	}
}

// pomoStatsTitle formats the "today" summary line.
func pomoStatsTitle(s pomoStats, err error) string {
	if err != nil {
		return "Today: —"
	}
	return fmt.Sprintf("Today: %d 🍅 · %d min · streak %d", s.Today.CompletedFocus, s.Today.FocusMin, s.Streak)
}
