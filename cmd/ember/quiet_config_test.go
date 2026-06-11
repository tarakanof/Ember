package main

import (
	"errors"
	"testing"
	"time"
)

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"22:00", 22 * 60, true},
		{"08:00", 8 * 60, true},
		{"00:00", 0, true},
		{"23:59", 23*60 + 59, true},
		{"24:00", 0, false},
		{"12:60", 0, false},
		{"aa:bb", 0, false},
		{"", 0, false},
		{"12.30", 0, false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ok := parseHHMM(c.in)
			if ok != c.ok || (ok && got != c.want) {
				t.Errorf("parseHHMM(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestQuietActive(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 6, 11, h, m, 0, 0, time.UTC)
	}
	cases := []struct {
		name       string
		start, end int
		t          time.Time
		want       bool
	}{
		{"overnight inside late", 22 * 60, 8 * 60, at(23, 0), true},
		{"overnight inside early", 22 * 60, 8 * 60, at(3, 0), true},
		{"overnight start boundary", 22 * 60, 8 * 60, at(22, 0), true},
		{"overnight end boundary excluded", 22 * 60, 8 * 60, at(8, 0), false},
		{"overnight outside", 22 * 60, 8 * 60, at(12, 0), false},
		{"normal inside", 13 * 60, 14 * 60, at(13, 30), true},
		{"normal start boundary", 13 * 60, 14 * 60, at(13, 0), true},
		{"normal end boundary excluded", 13 * 60, 14 * 60, at(14, 0), false},
		{"normal outside", 13 * 60, 14 * 60, at(9, 0), false},
		{"degenerate equal never quiet", 9 * 60, 9 * 60, at(9, 0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quietActive(c.start, c.end, c.t); got != c.want {
				t.Errorf("quietActive(%d,%d,%v) = %v want %v", c.start, c.end, c.t, got, c.want)
			}
		})
	}
}

func TestQuietHoursWindowDefaults(t *testing.T) {
	t.Run("zero config defaults to 22:00-08:00 disabled", func(t *testing.T) {
		var c Config
		enabled, start, end := c.quietHoursWindow()
		if enabled || start != 22*60 || end != 8*60 {
			t.Errorf("zero config = %v,%d,%d want false,1320,480", enabled, start, end)
		}
	})

	t.Run("explicit config returned as-is", func(t *testing.T) {
		c := Config{QuietHours: QuietHoursConfig{Enabled: true, Start: "23:30", End: "07:15"}}
		enabled, start, end := c.quietHoursWindow()
		if !enabled || start != 23*60+30 || end != 7*60+15 {
			t.Errorf("explicit config = %v,%d,%d want true,1410,435", enabled, start, end)
		}
	})
}

func TestValidateQuietHours(t *testing.T) {
	t.Run("empty fields valid (defaults apply)", func(t *testing.T) {
		if err := validateQuietHours(QuietHoursConfig{}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid explicit window accepted", func(t *testing.T) {
		if err := validateQuietHours(QuietHoursConfig{Start: "22:00", End: "08:00"}); err != nil {
			t.Errorf("valid window rejected: %v", err)
		}
	})

	t.Run("bad start rejected with sentinel", func(t *testing.T) {
		err := validateQuietHours(QuietHoursConfig{Start: "25:00"})
		if err == nil {
			t.Fatal("bad start accepted")
		}
		if !errors.Is(err, ErrConfigValidate) {
			t.Errorf("error not wrapped with ErrConfigValidate: %v", err)
		}
	})

	t.Run("bad end rejected with sentinel", func(t *testing.T) {
		err := validateQuietHours(QuietHoursConfig{End: "nope"})
		if err == nil {
			t.Fatal("bad end accepted")
		}
		if !errors.Is(err, ErrConfigValidate) {
			t.Errorf("error not wrapped with ErrConfigValidate: %v", err)
		}
	})
}
