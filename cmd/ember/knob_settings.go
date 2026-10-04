package main

import (
	"errors"
	"fmt"
	"regexp"
)

var knobPageIDs = []string{"bot", "pomodoro", "weather"}

var knobPageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)

type knobSettings struct {
	Brightness knobBrightness `json:"brightness"`
	Pages      []knobPage     `json:"pages"`
	Home       string         `json:"home"`
	PollMS     int            `json:"poll_ms"`
	Bot        knobBot        `json:"bot"`
}

type knobBrightness struct {
	FollowEmber bool `json:"follow_ember"`
	Level       int  `json:"level"`
	Floor       int  `json:"floor"`
	Startup     int  `json:"startup"`
}

type knobPage struct {
	ID string `json:"id"`
	On bool   `json:"on"`
}

type knobBot struct {
	SleepyAfterS int `json:"sleepy_after_s"`
	DemoHoldS    int `json:"demo_hold_s"`
}

func defaultKnobSettings() knobSettings {
	pages := make([]knobPage, 0, len(knobPageIDs))
	for _, id := range knobPageIDs {
		pages = append(pages, knobPage{ID: id, On: true})
	}
	return knobSettings{
		Brightness: knobBrightness{FollowEmber: true, Level: 153, Floor: 10, Startup: 153},
		Pages:      pages,
		Home:       "bot",
		PollMS:     2000,
		Bot:        knobBot{SleepyAfterS: 300, DemoHoldS: 20},
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

func inRange(field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s %d out of range [%d, %d]", field, v, lo, hi)
	}
	return nil
}
