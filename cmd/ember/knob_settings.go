package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

var knobPageIDs = []string{"bot", "pomodoro", "weather"}

var knobPageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)

type knobSettings struct {
	Brightness knobBrightness `json:"brightness"`
	Pages      []knobPage     `json:"pages"`
	Home       string         `json:"home"`
	PollMS     int            `json:"poll_ms"`
	Bot        knobBot        `json:"bot"`
	// Diagnostics is what the knob reports in a checkin's stats object:
	// off (nothing), basic or full. See ARCHITECTURE "Knob diagnostics".
	Diagnostics string `json:"diagnostics"`
	// StatsIntervalS is how often the knob sends stats (one of
	// knobStatsIntervals); LiveIntervalS its checkin period in live mode
	// (one of knobLiveIntervals). Chosen by measurement, see #249.
	StatsIntervalS int `json:"stats_interval_s"`
	LiveIntervalS  int `json:"live_interval_s"`
}

// Allowed stats and live-mode intervals, in seconds, and their defaults.
var (
	knobStatsIntervals = []int{30, 60, 120, 300}
	knobLiveIntervals  = []int{2, 5, 10}
)

const (
	knobStatsIntervalDefault = 60
	knobLiveIntervalDefault  = 5
)

const (
	knobDiagOff   = "off"
	knobDiagBasic = "basic"
	knobDiagFull  = "full"
)

type knobBrightness struct {
	FollowEmber bool `json:"follow_ember"`
	Level       int  `json:"level"`
	Floor       int  `json:"floor"`
	Startup     int  `json:"startup"`
}

// pageOn reports whether the page id is in the list and on.
func (s knobSettings) pageOn(id string) bool {
	for _, p := range s.Pages {
		if p.ID == id {
			return p.On
		}
	}
	return false
}

type knobPage struct {
	ID string `json:"id"`
	On bool   `json:"on"`
}

type knobBot struct {
	SleepyAfterS int `json:"sleepy_after_s"`
	DemoHoldS    int `json:"demo_hold_s"`
	// SourceLabel: the curved host label under the face (working, waiting,
	// error); WorkingRing: the glint orbiting the outline while working
	// (cinder#42). Pointers so a record stored before them reads as on.
	SourceLabel *bool `json:"source_label,omitempty"`
	WorkingRing *bool `json:"working_ring,omitempty"`
}

// clone copies the flags, so decoding a patch into a copy never writes
// through a pointer the stored record shares.
func (b knobBot) clone() knobBot {
	if b.SourceLabel != nil {
		v := *b.SourceLabel
		b.SourceLabel = &v
	}
	if b.WorkingRing != nil {
		v := *b.WorkingRing
		b.WorkingRing = &v
	}
	return b
}

func (s knobSettings) clone() knobSettings {
	s.Pages = slices.Clone(s.Pages)
	s.Bot = s.Bot.clone()
	return s
}

func defaultKnobSettings() knobSettings {
	pages := make([]knobPage, 0, len(knobPageIDs))
	for _, id := range knobPageIDs {
		pages = append(pages, knobPage{ID: id, On: true})
	}
	return knobSettings{
		Brightness:  knobBrightness{FollowEmber: true, Level: 153, Floor: 10, Startup: 153},
		Pages:       pages,
		Home:        "bot",
		PollMS:      2000,
		Bot:         knobBot{SleepyAfterS: 300, DemoHoldS: 20, SourceLabel: boolPtr(true), WorkingRing: boolPtr(true)},
		Diagnostics: knobDiagOff,

		StatsIntervalS: knobStatsIntervalDefault,
		LiveIntervalS:  knobLiveIntervalDefault,
	}
}

// fillDefaults sets fields a stored config from an older server lacks.
func (s *knobSettings) fillDefaults() {
	if s.Diagnostics == "" {
		s.Diagnostics = knobDiagOff
	}
	if s.StatsIntervalS == 0 {
		s.StatsIntervalS = knobStatsIntervalDefault
	}
	if s.LiveIntervalS == 0 {
		s.LiveIntervalS = knobLiveIntervalDefault
	}
	s.Bot.fillDefaults()
}

func (b *knobBot) fillDefaults() {
	if b.SourceLabel == nil {
		b.SourceLabel = boolPtr(true)
	}
	if b.WorkingRing == nil {
		b.WorkingRing = boolPtr(true)
	}
}

func (s knobSettings) validate() error {
	b := s.Brightness
	if err := inRange("brightness.level", b.Level, 0, 255); err != nil {
		return err
	}
	if err := inRange("brightness.floor", b.Floor, 1, 255); err != nil {
		return err
	}
	if b.Floor > b.Level {
		return fmt.Errorf("brightness.floor %d must be <= brightness.level %d", b.Floor, b.Level)
	}
	if err := inRange("brightness.startup", b.Startup, 0, 255); err != nil {
		return err
	}
	if err := s.validatePages(); err != nil {
		return err
	}
	if err := inRange("poll_ms", s.PollMS, 1000, 10000); err != nil {
		return err
	}
	switch s.Diagnostics {
	case knobDiagOff, knobDiagBasic, knobDiagFull:
	default:
		return fmt.Errorf("diagnostics %q must be off, basic or full", s.Diagnostics)
	}
	if err := oneOf("stats_interval_s", s.StatsIntervalS, knobStatsIntervals); err != nil {
		return err
	}
	if err := oneOf("live_interval_s", s.LiveIntervalS, knobLiveIntervals); err != nil {
		return err
	}
	if err := inRange("bot.sleepy_after_s", s.Bot.SleepyAfterS, 0, 86400); err != nil {
		return err
	}
	return inRange("bot.demo_hold_s", s.Bot.DemoHoldS, 1, 600)
}

func (s knobSettings) validatePages() error {
	seen := make(map[string]bool, len(s.Pages))
	homeOn := false
	for _, p := range s.Pages {
		if !knobPageIDPattern.MatchString(p.ID) {
			return fmt.Errorf("page id %q must match %s", p.ID, knobPageIDPattern)
		}
		if seen[p.ID] {
			return fmt.Errorf("page %q listed twice", p.ID)
		}
		seen[p.ID] = true
		if p.ID == s.Home && p.On {
			homeOn = true
		}
	}
	if !homeOn {
		return errors.New("home must name a page that is on")
	}
	return nil
}

func oneOf(field string, v int, allowed []int) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%s %d must be one of %v", field, v, allowed)
	}
	return nil
}

func inRange(field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s %d out of range [%d, %d]", field, v, lo, hi)
	}
	return nil
}
