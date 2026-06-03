package main

import (
	"errors"
	"testing"
)

func TestPomodoroDefaultsApplied(t *testing.T) {
	var c Config
	c.applyDefaults()
	p := c.Pomodoro
	if p.FocusMinutes != 25 || p.ShortBreakMinutes != 5 || p.LongBreakMinutes != 15 {
		t.Fatalf("durations = %d/%d/%d, want 25/5/15", p.FocusMinutes, p.ShortBreakMinutes, p.LongBreakMinutes)
	}
	if p.RoundsBeforeLongBreak != 4 {
		t.Fatalf("rounds = %d, want 4", p.RoundsBeforeLongBreak)
	}
	if p.FocusColor == "" || p.BreakColor == "" {
		t.Fatalf("colors not defaulted: %q / %q", p.FocusColor, p.BreakColor)
	}
	if p.DBPath == "" {
		t.Fatal("db_path not defaulted")
	}
}

func TestPomodoroValidationRejectsBadDuration(t *testing.T) {
	c := defaultConfig()
	c.applyDefaults()
	c.Pomodoro.FocusMinutes = 999
	if err := validateConfig(c); !errors.Is(err, ErrConfigValidate) {
		t.Fatalf("expected validate error for focus=999, got %v", err)
	}
}

func TestPomodoroValidationRejectsBadColor(t *testing.T) {
	c := defaultConfig()
	c.applyDefaults()
	c.Pomodoro.FocusColor = "tomato"
	if err := validateConfig(c); !errors.Is(err, ErrConfigValidate) {
		t.Fatalf("expected validate error for bad color, got %v", err)
	}
}

func TestPomodoroValidationAcceptsDefaults(t *testing.T) {
	c := defaultConfig()
	c.applyDefaults()
	if err := validateConfig(c); err != nil {
		t.Fatalf("default pomodoro config should validate, got %v", err)
	}
}
