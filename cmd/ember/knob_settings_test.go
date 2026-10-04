package main

import (
	"encoding/json"
	"testing"
)

func TestKnobSettingsDefaultsAreValid(t *testing.T) {
	if err := defaultKnobSettings().validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestKnobSettingsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*knobSettings)
		ok     bool
	}{
		{"defaults", func(*knobSettings) {}, true},
		{"diagnostics basic", func(s *knobSettings) { s.Diagnostics = "basic" }, true},
		{"diagnostics full", func(s *knobSettings) { s.Diagnostics = "full" }, true},
		{"diagnostics unknown", func(s *knobSettings) { s.Diagnostics = "verbose" }, false},
		{"diagnostics empty", func(s *knobSettings) { s.Diagnostics = "" }, false},
		{"level above 255", func(s *knobSettings) { s.Brightness.Level = 256 }, false},
		{"level negative", func(s *knobSettings) { s.Brightness.Level = -1 }, false},
		{"floor zero", func(s *knobSettings) { s.Brightness.Floor = 0 }, false},
		{"floor above level", func(s *knobSettings) { s.Brightness.Level = 20; s.Brightness.Floor = 30 }, false},
		{"floor equals level", func(s *knobSettings) { s.Brightness.Level = 30; s.Brightness.Floor = 30 }, true},
		{"startup above 255", func(s *knobSettings) { s.Brightness.Startup = 300 }, false},
		{"startup zero", func(s *knobSettings) { s.Brightness.Startup = 0 }, true},
		{"no pages", func(s *knobSettings) { s.Pages = nil }, false},
		{"all pages off", func(s *knobSettings) {
			for i := range s.Pages {
				s.Pages[i].On = false
			}
		}, false},
		{"new page id", func(s *knobSettings) { s.Pages = append(s.Pages, knobPage{ID: "clock_2", On: true}) }, true},
		{"malformed page id", func(s *knobSettings) { s.Pages = append(s.Pages, knobPage{ID: "Clock", On: true}) }, false},
		{"empty page id", func(s *knobSettings) { s.Pages = append(s.Pages, knobPage{ID: "", On: true}) }, false},
		{"long page id", func(s *knobSettings) { s.Pages = append(s.Pages, knobPage{ID: "a234567890123456x", On: true}) }, false},
		{"duplicate page", func(s *knobSettings) { s.Pages = append(s.Pages, knobPage{ID: "bot", On: true}) }, false},
		{"subset of pages", func(s *knobSettings) { s.Pages = []knobPage{{ID: "pomodoro", On: true}}; s.Home = "pomodoro" }, true},
		{"reordered pages", func(s *knobSettings) {
			s.Pages = []knobPage{{ID: "weather", On: true}, {ID: "bot", On: true}, {ID: "pomodoro", On: false}}
		}, true},
		{"home not listed", func(s *knobSettings) { s.Home = "clock" }, false},
		{"home page off", func(s *knobSettings) { s.Pages[0].On = false }, false},
		{"poll too fast", func(s *knobSettings) { s.PollMS = 999 }, false},
		{"poll too slow", func(s *knobSettings) { s.PollMS = 10001 }, false},
		{"poll bounds", func(s *knobSettings) { s.PollMS = 1000 }, true},
		{"sleepy off", func(s *knobSettings) { s.Bot.SleepyAfterS = 0 }, true},
		{"sleepy too long", func(s *knobSettings) { s.Bot.SleepyAfterS = 86401 }, false},
		{"demo hold zero", func(s *knobSettings) { s.Bot.DemoHoldS = 0 }, false},
		{"demo hold too long", func(s *knobSettings) { s.Bot.DemoHoldS = 601 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := defaultKnobSettings()
			c.mutate(&s)
			err := s.validate()
			if c.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestKnobSettingsWireShape(t *testing.T) {
	b, err := json.Marshal(defaultKnobSettings())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},` +
		`"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true}],` +
		`"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20},"diagnostics":"off",` +
		`"stats_interval_s":60,"live_interval_s":5}`
	if string(b) != want {
		t.Fatalf("wire shape\n got %s\nwant %s", b, want)
	}
}
