package main

import (
	"encoding/json"
	"fmt"
	"reflect"
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
		{"quiet calm off", func(s *knobSettings) { s.Quiet.Calm = boolPtr(false) }, true},
		{"quiet dim bounds", func(s *knobSettings) { s.Quiet.DimLevel = new(1) }, true},
		{"quiet dim full", func(s *knobSettings) { s.Quiet.DimLevel = new(255) }, true},
		{"quiet dim zero", func(s *knobSettings) { s.Quiet.DimLevel = new(0) }, false},
		{"quiet dim above 255", func(s *knobSettings) { s.Quiet.DimLevel = new(256) }, false},
		{"quiet dim below floor", func(s *knobSettings) { s.Brightness.Floor = 40; s.Quiet.DimLevel = new(5) }, true},
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
		`"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":false}],` +
		`"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20,"source_label":true,"working_ring":true},"diagnostics":"off",` +
		`"stats_interval_s":60,"live_interval_s":5,"display":{"fast_link":true},"quiet":{"calm":true,"dim_level":20}}`
	if string(b) != want {
		t.Fatalf("wire shape\n got %s\nwant %s", b, want)
	}
}

func TestKnobNowPlayingPageIsKnownAndOffByDefault(t *testing.T) {
	d := defaultKnobSettings()
	if d.pageOn("nowplaying") || !d.pageOn("weather") {
		t.Fatalf("defaults: %+v", d.Pages)
	}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	s := knobSettings{Pages: []knobPage{{ID: "weather", On: true}, {ID: "bot", On: false}, {ID: "x", On: true}}}
	s.fillDefaults()
	want := []knobPage{{ID: "weather", On: true}, {ID: "bot", On: false}, {ID: "x", On: true},
		{ID: "pomodoro", On: false}, {ID: "nowplaying", On: false}}
	if len(s.Pages) != len(want) {
		t.Fatalf("pages = %+v", s.Pages)
	}
	for i := range want {
		if s.Pages[i] != want[i] {
			t.Fatalf("pages = %+v, want %+v", s.Pages, want)
		}
	}
	s = defaultKnobSettings()
	s.Pages = []knobPage{{ID: "nowplaying", On: true}, {ID: "bot", On: true}, {ID: "pomodoro", On: true}, {ID: "weather", On: true}}
	s.fillDefaults()
	if len(s.Pages) != 4 || s.Pages[0] != (knobPage{ID: "nowplaying", On: true}) {
		t.Fatalf("pages = %+v", s.Pages)
	}
}

func TestKnobPagesCapAtEight(t *testing.T) {
	s := defaultKnobSettings()
	s.Pages = nil
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		s.Pages = append(s.Pages, knobPage{ID: id, On: true})
	}
	s.Home = "a"
	if err := s.validate(); err != nil {
		t.Fatalf("8 pages: %v", err)
	}
	s.fillDefaults()
	if len(s.Pages) != 8 {
		t.Fatalf("fill grew a full list: %d", len(s.Pages))
	}
	s.Pages = append(s.Pages, knobPage{ID: "i", On: true})
	if err := s.validate(); err == nil {
		t.Fatal("9 pages accepted")
	}
}

func TestKnobQuietDefaultsCalmAndNightLevel(t *testing.T) {
	d := defaultKnobSettings()
	if d.Quiet.Calm == nil || !*d.Quiet.Calm || d.Quiet.DimLevel == nil || *d.Quiet.DimLevel != (BrightnessConfig{}).resolved().NightLevel {
		t.Fatalf("defaults: %+v", d.Quiet)
	}
	var s knobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.fillDefaults()
	if !reflect.DeepEqual(s.Quiet, d.Quiet) {
		t.Fatalf("a stored record without quiet reads %+v, want the defaults", s.Quiet)
	}
}

func TestKnobQuietMergesFieldByField(t *testing.T) {
	cur := defaultKnobSettings()
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"quiet":{"dim_level":5}}`))
	if err != nil || *got.Quiet.DimLevel != 5 || !*got.Quiet.Calm {
		t.Fatalf("merge: %v %+v", err, got.Quiet)
	}
	if *cur.Quiet.DimLevel != knobQuietDimDefault {
		t.Fatal("merging into a clone wrote through to the original")
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"quiet":{"calm":false,"dim_level":null}}`))
	if err != nil || *got.Quiet.Calm || *got.Quiet.DimLevel != knobQuietDimDefault {
		t.Fatalf("null dim_level must read as the default: %v %+v", err, got.Quiet)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var back knobSettings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.fillDefaults()
	if !reflect.DeepEqual(back.Quiet, got.Quiet) {
		t.Fatalf("round trip %+v, want %+v", back.Quiet, got.Quiet)
	}
	if _, err := mergeKnobSettings(cur.clone(), []byte(`{"quiet":{"mute":true}}`)); err == nil {
		t.Fatal("unknown quiet field accepted")
	}
}

const cinderCfgSettingsMax = 1024

func TestKnobConfigFitsTheFirmwareStore(t *testing.T) {
	s := defaultKnobSettings()
	s.Pages = nil
	for i := range knobMaxPages {
		s.Pages = append(s.Pages, knobPage{ID: fmt.Sprintf("page-%011d", i), On: false})
	}
	s.Pages[0].On, s.Home = true, s.Pages[0].ID
	s.Brightness = knobBrightness{FollowEmber: false, Level: 255, Floor: 255, Startup: 255}
	s.PollMS, s.Diagnostics, s.StatsIntervalS, s.LiveIntervalS = 10000, knobDiagBasic, 300, 10
	s.Bot.SleepyAfterS, s.Bot.DemoHoldS = 86400, 600
	s.Bot.SourceLabel, s.Bot.WorkingRing = boolPtr(false), boolPtr(false)
	s.Display.FastLink, s.Quiet.Calm, s.Quiet.DimLevel = boolPtr(false), boolPtr(false), new(255)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.validate(); err != nil {
		t.Fatalf("the largest valid config (%d B) is rejected: %v", len(b), err)
	}
	if len(b) > cinderCfgSettingsMax {
		t.Fatalf("the largest valid config is %d B, over the knob's %d", len(b), cinderCfgSettingsMax)
	}
	t.Logf("largest valid config: %d B of %d", len(b), cinderCfgSettingsMax)
}
