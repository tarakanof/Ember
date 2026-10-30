package devices

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func TestKnobSettingsDiagnosticsDefaultsOff(t *testing.T) {
	if got := DefaultKnobSettings().Diagnostics; got != "off" {
		t.Fatalf("default diagnostics = %q, want off", got)
	}
}

func TestKnobSettingsIntervalDefaults(t *testing.T) {
	d := DefaultKnobSettings()
	if d.StatsIntervalS != KnobStatsIntervalDefault || d.LiveIntervalS != KnobLiveIntervalDefault {
		t.Fatalf("defaults = %d/%d, want %d/%d", d.StatsIntervalS, d.LiveIntervalS, KnobStatsIntervalDefault, KnobLiveIntervalDefault)
	}
	if !slices.Contains(knobStatsIntervals, d.StatsIntervalS) || !slices.Contains(knobLiveIntervals, d.LiveIntervalS) {
		t.Fatalf("defaults %d/%d not in the allowed sets", d.StatsIntervalS, d.LiveIntervalS)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestKnobSettingsDefaultsAreValid(t *testing.T) {
	if err := DefaultKnobSettings().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestKnobSettingsValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*KnobSettings)
		ok     bool
	}{
		{"defaults", func(*KnobSettings) {}, true},
		{"diagnostics basic", func(s *KnobSettings) { s.Diagnostics = "basic" }, true},
		{"diagnostics full", func(s *KnobSettings) { s.Diagnostics = "full" }, true},
		{"diagnostics unknown", func(s *KnobSettings) { s.Diagnostics = "verbose" }, false},
		{"diagnostics empty", func(s *KnobSettings) { s.Diagnostics = "" }, false},
		{"level above 255", func(s *KnobSettings) { s.Brightness.Level = 256 }, false},
		{"level negative", func(s *KnobSettings) { s.Brightness.Level = -1 }, false},
		{"floor zero", func(s *KnobSettings) { s.Brightness.Floor = 0 }, false},
		{"floor above level", func(s *KnobSettings) { s.Brightness.Level = 20; s.Brightness.Floor = 30 }, false},
		{"floor equals level", func(s *KnobSettings) { s.Brightness.Level = 30; s.Brightness.Floor = 30 }, true},
		{"startup above 255", func(s *KnobSettings) { s.Brightness.Startup = 300 }, false},
		{"startup zero", func(s *KnobSettings) { s.Brightness.Startup = 0 }, true},
		{"no pages", func(s *KnobSettings) { s.Pages = nil }, false},
		{"all pages off", func(s *KnobSettings) {
			for i := range s.Pages {
				s.Pages[i].On = false
			}
		}, false},
		{"new page id", func(s *KnobSettings) { s.Pages = append(s.Pages, KnobPage{ID: "clock_2", On: true}) }, true},
		{"malformed page id", func(s *KnobSettings) { s.Pages = append(s.Pages, KnobPage{ID: "Clock", On: true}) }, false},
		{"empty page id", func(s *KnobSettings) { s.Pages = append(s.Pages, KnobPage{ID: "", On: true}) }, false},
		{"long page id", func(s *KnobSettings) { s.Pages = append(s.Pages, KnobPage{ID: "a234567890123456x", On: true}) }, false},
		{"duplicate page", func(s *KnobSettings) { s.Pages = append(s.Pages, KnobPage{ID: "bot", On: true}) }, false},
		{"subset of pages", func(s *KnobSettings) { s.Pages = []KnobPage{{ID: "pomodoro", On: true}}; s.Home = "pomodoro" }, true},
		{"reordered pages", func(s *KnobSettings) {
			s.Pages = []KnobPage{{ID: "weather", On: true}, {ID: "bot", On: true}, {ID: "pomodoro", On: false}}
		}, true},
		{"home not listed", func(s *KnobSettings) { s.Home = "clock" }, false},
		{"home page off", func(s *KnobSettings) { s.Pages[0].On = false }, false},
		{"poll too fast", func(s *KnobSettings) { s.PollMS = 999 }, false},
		{"poll too slow", func(s *KnobSettings) { s.PollMS = 10001 }, false},
		{"poll bounds", func(s *KnobSettings) { s.PollMS = 1000 }, true},
		{"sleepy off", func(s *KnobSettings) { s.Bot.SleepyAfterS = 0 }, true},
		{"sleepy too long", func(s *KnobSettings) { s.Bot.SleepyAfterS = 86401 }, false},
		{"demo hold zero", func(s *KnobSettings) { s.Bot.DemoHoldS = 0 }, false},
		{"demo hold too long", func(s *KnobSettings) { s.Bot.DemoHoldS = 601 }, false},
		{"weather moments off", func(s *KnobSettings) { s.Bot.WeatherMoments = boolPtr(false) }, true},
		{"weather every 0", func(s *KnobSettings) { s.Bot.WeatherEveryMin = new(0) }, true},
		{"weather every 240", func(s *KnobSettings) { s.Bot.WeatherEveryMin = new(240) }, true},
		{"weather every 241", func(s *KnobSettings) { s.Bot.WeatherEveryMin = new(241) }, false},
		{"weather every negative", func(s *KnobSettings) { s.Bot.WeatherEveryMin = new(-1) }, false},
		{"quiet calm off", func(s *KnobSettings) { s.Quiet.Calm = boolPtr(false) }, true},
		{"quiet dim bounds", func(s *KnobSettings) { s.Quiet.DimLevel = new(1) }, true},
		{"quiet dim full", func(s *KnobSettings) { s.Quiet.DimLevel = new(255) }, true},
		{"quiet dim zero", func(s *KnobSettings) { s.Quiet.DimLevel = new(0) }, false},
		{"quiet dim above 255", func(s *KnobSettings) { s.Quiet.DimLevel = new(256) }, false},
		{"quiet dim below floor", func(s *KnobSettings) { s.Brightness.Floor = 40; s.Quiet.DimLevel = new(5) }, true},
		{"rotation 90", func(s *KnobSettings) { s.Display.Rotation = 90 }, true},
		{"rotation 180", func(s *KnobSettings) { s.Display.Rotation = 180 }, true},
		{"rotation 270", func(s *KnobSettings) { s.Display.Rotation = 270 }, true},
		{"rotation 45", func(s *KnobSettings) { s.Display.Rotation = 45 }, false},
		{"rotation 360", func(s *KnobSettings) { s.Display.Rotation = 360 }, false},
		{"rotation negative", func(s *KnobSettings) { s.Display.Rotation = -90 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := DefaultKnobSettings()
			c.mutate(&s)
			err := s.Validate()
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
	b, err := json.Marshal(DefaultKnobSettings())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},` +
		`"pages":[{"id":"bot","on":true},{"id":"day","on":true},{"id":"weather","on":true},{"id":"room","on":false},{"id":"nowplaying","on":false},{"id":"controls","on":false},{"id":"pomodoro","on":false}],` +
		`"home":"bot","swipe_pages":true,"poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20,"source_label":true,"working_ring":true,"weather_moments":true,"weather_every_min":30},"diagnostics":"off",` +
		`"stats_interval_s":60,"live_interval_s":5,"display":{"fast_link":true,"rotation":0},"quiet":{"calm":true,"dim_level":20}}`
	if string(b) != want {
		t.Fatalf("wire shape\n got %s\nwant %s", b, want)
	}
}

func TestKnobNowPlayingPageIsKnownAndOffByDefault(t *testing.T) {
	d := DefaultKnobSettings()
	if d.PageOn("room") || d.PageOn("nowplaying") || d.PageOn("controls") || d.PageOn("pomodoro") || !d.PageOn("weather") || !d.PageOn("day") {
		t.Fatalf("defaults: %+v", d.Pages)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	s := KnobSettings{Pages: []KnobPage{{ID: "weather", On: true}, {ID: "bot", On: false}, {ID: "x", On: true}}}
	s.FillDefaults()
	want := []KnobPage{{ID: "weather", On: true}, {ID: "bot", On: false}, {ID: "x", On: true},
		{ID: "day", On: false}, {ID: "room", On: false}, {ID: "nowplaying", On: false}, {ID: "controls", On: false}, {ID: "pomodoro", On: false}}
	if len(s.Pages) != len(want) {
		t.Fatalf("pages = %+v", s.Pages)
	}
	for i := range want {
		if s.Pages[i] != want[i] {
			t.Fatalf("pages = %+v, want %+v", s.Pages, want)
		}
	}
	s = DefaultKnobSettings()
	s.Pages = []KnobPage{{ID: "nowplaying", On: true}, {ID: "bot", On: true}, {ID: "pomodoro", On: true}, {ID: "weather", On: true}, {ID: "day", On: true}}
	s.FillDefaults()
	if len(s.Pages) != len(KnobPageIDs) || s.Pages[0] != (KnobPage{ID: "nowplaying", On: true}) {
		t.Fatalf("pages = %+v", s.Pages)
	}
}

func TestKnobPagesCapAtEight(t *testing.T) {
	s := DefaultKnobSettings()
	s.Pages = nil
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		s.Pages = append(s.Pages, KnobPage{ID: id, On: true})
	}
	s.Home = "a"
	if err := s.Validate(); err != nil {
		t.Fatalf("8 pages: %v", err)
	}
	s.FillDefaults()
	if len(s.Pages) != 8 {
		t.Fatalf("fill grew a full list: %d", len(s.Pages))
	}
	s.Pages = append(s.Pages, KnobPage{ID: "i", On: true})
	if err := s.Validate(); err == nil {
		t.Fatal("9 pages accepted")
	}
}

func TestKnobQuietMergesFieldByField(t *testing.T) {
	cur := DefaultKnobSettings()
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"quiet":{"dim_level":5}}`))
	if err != nil || *got.Quiet.DimLevel != 5 || !*got.Quiet.Calm {
		t.Fatalf("merge: %v %+v", err, got.Quiet)
	}
	if *cur.Quiet.DimLevel != KnobQuietDimDefault {
		t.Fatal("merging into a clone wrote through to the original")
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"quiet":{"calm":false,"dim_level":null}}`))
	if err != nil || *got.Quiet.Calm || *got.Quiet.DimLevel != KnobQuietDimDefault {
		t.Fatalf("null dim_level must read as the default: %v %+v", err, got.Quiet)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var back KnobSettings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.FillDefaults()
	if !reflect.DeepEqual(back.Quiet, got.Quiet) {
		t.Fatalf("round trip %+v, want %+v", back.Quiet, got.Quiet)
	}
	if _, err := mergeKnobSettings(cur.clone(), []byte(`{"quiet":{"mute":true}}`)); err == nil {
		t.Fatal("unknown quiet field accepted")
	}
}

const (
	legacyCfgSettingsMax = 1024
	knobCfgSettingsMax   = 2048
)

func TestKnobConfigFitsTheFirmwareStore(t *testing.T) {
	s := DefaultKnobSettings()
	s.Pages = nil
	for i := range KnobMaxPages {
		s.Pages = append(s.Pages, KnobPage{ID: fmt.Sprintf("page-%011d", i), On: false})
	}
	s.Pages[0].On, s.Home = true, s.Pages[0].ID
	s.Brightness = KnobBrightness{FollowEmber: false, Level: 255, Floor: 255, Startup: 255}
	s.PollMS, s.Diagnostics, s.StatsIntervalS, s.LiveIntervalS = 10000, KnobDiagBasic, 300, 10
	s.Bot.SleepyAfterS, s.Bot.DemoHoldS = 86400, 600
	s.Bot.SourceLabel, s.Bot.WorkingRing = boolPtr(false), boolPtr(false)
	s.Bot.WeatherMoments, s.Bot.WeatherEveryMin = boolPtr(false), new(240)
	s.Display.FastLink, s.Quiet.Calm, s.Quiet.DimLevel = boolPtr(false), boolPtr(false), new(255)
	s.Display.Rotation = 270
	s.SwipePages = boolPtr(false)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("the largest valid config (%d B) is rejected: %v", len(b), err)
	}
	if len(b) > legacyCfgSettingsMax {
		t.Fatalf("the largest valid config is %d B, over the knob's %d", len(b), legacyCfgSettingsMax)
	}
	t.Logf("largest valid config: %d B of %d", len(b), legacyCfgSettingsMax)
}

func TestKnobV2KeysValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*KnobSettings)
		ok     bool
	}{
		{"dim never", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(0)} }, true},
		{"dim 30", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(30)} }, true},
		{"dim 29", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(29)} }, false},
		{"dim 86400", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(86400)} }, true},
		{"dim 86401", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(86401)} }, false},
		{"dim negative", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(-1)} }, false},
		{"level 0", func(s *KnobSettings) { s.Idle = &KnobIdle{DimLevel: new(0)} }, false},
		{"level 1", func(s *KnobSettings) { s.Idle = &KnobIdle{DimLevel: new(1)} }, true},
		{"level 255", func(s *KnobSettings) { s.Idle = &KnobIdle{DimLevel: new(255)} }, true},
		{"level 256", func(s *KnobSettings) { s.Idle = &KnobIdle{DimLevel: new(256)} }, false},
		{"off never", func(s *KnobSettings) { s.Idle = &KnobIdle{OffAfterS: new(0)} }, true},
		{"off 59", func(s *KnobSettings) { s.Idle = &KnobIdle{OffAfterS: new(59)} }, false},
		{"off 60", func(s *KnobSettings) { s.Idle = &KnobIdle{OffAfterS: new(60)} }, true},
		{"off 86401", func(s *KnobSettings) { s.Idle = &KnobIdle{OffAfterS: new(86401)} }, false},
		{"off after dim", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(600), OffAfterS: new(601)} }, true},
		{"off equals dim", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(600), OffAfterS: new(600)} }, false},
		{"off before dim", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(600), OffAfterS: new(300)} }, false},
		{"off with dim never", func(s *KnobSettings) { s.Idle = &KnobIdle{DimAfterS: new(0), OffAfterS: new(60)} }, true},
		{"colour upper", func(s *KnobSettings) { s.Apps = &KnobApps{Pomodoro: &KnobPomoApp{FocusColor: new("#FF6A3D")}} }, true},
		{"colour lower", func(s *KnobSettings) { s.Apps = &KnobApps{Pomodoro: &KnobPomoApp{BreakColor: new("#4fa9ff")}} }, true},
		{"colour no hash", func(s *KnobSettings) { s.Apps = &KnobApps{Pomodoro: &KnobPomoApp{FocusColor: new("FF6A3D")}} }, false},
		{"colour short", func(s *KnobSettings) { s.Apps = &KnobApps{Pomodoro: &KnobPomoApp{FocusColor: new("#FFF")}} }, false},
		{"colour not hex", func(s *KnobSettings) { s.Apps = &KnobApps{Pomodoro: &KnobPomoApp{FocusColor: new("#GGGGGG")}} }, false},
		{"colour alpha", func(s *KnobSettings) { s.Apps = &KnobApps{Pomodoro: &KnobPomoApp{FocusColor: new("#FF6A3DFF")}} }, false},
		{"faces one", func(s *KnobSettings) { s.Apps = &KnobApps{Weather: &KnobWeatherApp{Faces: []string{"sun"}}} }, true},
		{"faces all", func(s *KnobSettings) { s.Apps = &KnobApps{Weather: &KnobWeatherApp{Faces: KnobWeatherFaces}} }, true},
		{"faces empty", func(s *KnobSettings) { s.Apps = &KnobApps{Weather: &KnobWeatherApp{Faces: []string{}}} }, false},
		{"faces unknown", func(s *KnobSettings) { s.Apps = &KnobApps{Weather: &KnobWeatherApp{Faces: []string{"snow"}}} }, false},
		{"faces duplicate", func(s *KnobSettings) { s.Apps = &KnobApps{Weather: &KnobWeatherApp{Faces: []string{"sun", "sun"}}} }, false},
		{"faces eight", func(s *KnobSettings) {
			s.Apps = &KnobApps{Weather: &KnobWeatherApp{Faces: append([]string{"sun"}, KnobWeatherFaces...)}}
		}, false},
		{"np dim 59", func(s *KnobSettings) { s.Apps = &KnobApps{NowPlaying: &KnobNPApp{DimAfterS: new(59)}} }, false},
		{"np dim 60", func(s *KnobSettings) { s.Apps = &KnobApps{NowPlaying: &KnobNPApp{DimAfterS: new(60)}} }, true},
		{"np dim 1800", func(s *KnobSettings) { s.Apps = &KnobApps{NowPlaying: &KnobNPApp{DimAfterS: new(1800)}} }, true},
		{"np dim 1801", func(s *KnobSettings) { s.Apps = &KnobApps{NowPlaying: &KnobNPApp{DimAfterS: new(1801)}} }, false},
		{"room empty", func(s *KnobSettings) { s.Room = &KnobRoom{} }, true},
		{"presence off never", func(s *KnobSettings) { s.Room = &KnobRoom{PresenceOffS: new(0)} }, true},
		{"presence off 86399", func(s *KnobSettings) { s.Room = &KnobRoom{PresenceOffS: new(86399)} }, true},
		{"presence off 86400", func(s *KnobSettings) { s.Room = &KnobRoom{PresenceOffS: new(86400)} }, false},
		{"presence off negative", func(s *KnobSettings) { s.Room = &KnobRoom{PresenceOffS: new(-1)} }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := DefaultKnobSettings()
			c.mutate(&s)
			err := s.Validate()
			if c.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestKnobV2RejectsUnknownKeysAndPages(t *testing.T) {
	for _, patch := range []string{
		`{"idle":{"sleep_after_s":5}}`,
		`{"apps":{"bot":{"x":1}}}`,
		`{"apps":{"pomodoro":{"ring_color":"#FFFFFF"}}}`,
		`{"apps":{"weather":{"units":"c"}}}`,
		`{"apps":{"nowplaying":{"brightness":5}}}`,
		`{"room":{"presence_on_s":5}}`,
		`{"room":{"presence_off_s":1.5}}`,
		`{"room":{"enabled":"yes"}}`,
	} {
		if _, err := mergeKnobSettings(DefaultKnobSettings(), []byte(patch)); err == nil {
			t.Errorf("%s accepted", patch)
		}
	}
}

func TestKnobV2MergeFieldByField(t *testing.T) {
	cur := DefaultKnobSettings()
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"idle":{"dim_after_s":600,"dim_level":40}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"idle":{"off_after_s":1800}}`))
	if err != nil {
		t.Fatal(err)
	}
	if i := got.Idle; *i.DimAfterS != 600 || *i.DimLevel != 40 || *i.OffAfterS != 1800 {
		t.Fatalf("idle patch lost fields: %+v", i)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"apps":{"weather":{"faces":["sun","temp","rain"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	prev := got.clone()
	got, err = mergeKnobSettings(got.clone(), []byte(`{"apps":{"weather":{"faces":["air"]},"pomodoro":{"focus_color":"#aabbcc"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Apps.Weather.Faces, []string{"air"}) {
		t.Fatalf("faces must replace whole: %v", got.Apps.Weather.Faces)
	}
	if *got.Apps.Pomodoro.FocusColor != "#AABBCC" || got.Apps.Pomodoro.BreakColor != nil {
		t.Fatalf("colour not upper-cased or stray break colour: %+v", got.Apps.Pomodoro)
	}
	if !reflect.DeepEqual(prev.Apps.Weather.Faces, []string{"sun", "temp", "rain"}) || prev.Apps.Pomodoro != nil {
		t.Fatalf("merging into a clone wrote through: %+v", prev.Apps)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"idle":{"dim_level":null},"apps":{"pomodoro":null}}`))
	if err != nil || got.Idle.DimLevel != nil || got.Apps.Pomodoro != nil || *got.Idle.DimAfterS != 600 {
		t.Fatalf("null must clear: %v %+v %+v", err, got.Idle, got.Apps)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{}`))
	if err != nil {
		t.Fatalf("{} patch: %v", err)
	}
}

func TestKnobV2ClearingTheLastFieldReturnsToAbsent(t *testing.T) {
	cur := DefaultKnobSettings()
	legacy, _ := json.Marshal(cur)
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"idle":{"dim_level":40},"apps":{"pomodoro":{"focus_color":"#aabbcc"},"weather":{"faces":["sun"]},"nowplaying":{"dim_after_s":60}},"room":{"enabled":true}}`))
	if err != nil || got.Idle == nil || got.Apps == nil || got.Room == nil {
		t.Fatalf("set: %v %+v", err, got)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"idle":{"dim_level":null},"apps":{"pomodoro":{"focus_color":null},"weather":{"faces":null},"nowplaying":{"dim_after_s":null}},"room":{"enabled":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if now, _ := json.Marshal(got); string(now) != string(legacy) {
		t.Fatalf("clearing every field left residue\n got %s\nwant %s", now, legacy)
	}
}

func maxLegacyStoreSettings() KnobSettings {
	s := DefaultKnobSettings()
	s.Pages = nil
	for i := range KnobMaxPages {
		s.Pages = append(s.Pages, KnobPage{ID: fmt.Sprintf("page-%011d", i), On: false})
	}
	s.Pages[0].On, s.Home = true, s.Pages[0].ID
	s.Brightness = KnobBrightness{FollowEmber: false, Level: 255, Floor: 255, Startup: 255}
	s.PollMS, s.Diagnostics, s.StatsIntervalS, s.LiveIntervalS = 10000, KnobDiagBasic, 300, 10
	s.Bot.SleepyAfterS, s.Bot.DemoHoldS = 86400, 600
	s.Bot.SourceLabel, s.Bot.WorkingRing = boolPtr(false), boolPtr(false)
	s.Bot.WeatherMoments, s.Bot.WeatherEveryMin = boolPtr(false), new(240)
	s.Display.FastLink, s.Quiet.Calm, s.Quiet.DimLevel = boolPtr(false), boolPtr(false), new(255)
	s.Display.Rotation = 270
	s.SwipePages = boolPtr(false)
	s.Idle = &KnobIdle{DimAfterS: new(86399), DimLevel: new(255), OffAfterS: new(86400)}
	s.Apps = &KnobApps{
		Pomodoro:   &KnobPomoApp{FocusColor: new("#FFFFFF"), BreakColor: new("#FFFFFF")},
		Weather:    &KnobWeatherApp{Faces: slices.Clone(KnobWeatherFaces)},
		NowPlaying: &KnobNPApp{DimAfterS: new(1800)},
	}
	return s
}

func maxV2Settings() KnobSettings {
	s := maxLegacyStoreSettings()
	s.Room = &KnobRoom{Enabled: boolPtr(false), PresenceOffS: new(86399), PresenceWake: boolPtr(false), Moments: boolPtr(false)}
	return s
}

func jsonDepth(v any) int {
	switch x := v.(type) {
	case map[string]any:
		d := 0
		for _, c := range x {
			d = max(d, jsonDepth(c))
		}
		return d + 1
	case []any:
		d := 0
		for _, c := range x {
			d = max(d, jsonDepth(c))
		}
		return d + 1
	}
	return 0
}

const cinderConfigMaxDepth = 4

func configDepth(t *testing.T, b []byte) int {
	t.Helper()
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		t.Fatal(err)
	}
	return jsonDepth(tree)
}

func TestKnobV2LargestConfigFitsDepthAndSize(t *testing.T) {
	cases := []struct {
		name  string
		s     KnobSettings
		limit int
	}{
		{"without the room-only keys", maxLegacyStoreSettings(), legacyCfgSettingsMax},
		{"with them", maxV2Settings(), knobCfgSettingsMax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.s.Validate(); err != nil {
				t.Fatalf("the largest v2 config is rejected: %v", err)
			}
			b, err := json.Marshal(c.s)
			if err != nil {
				t.Fatal(err)
			}
			if len(b) > c.limit {
				t.Fatalf("the largest valid v2 config is %d B, over the knob's %d", len(b), c.limit)
			}
			if d := configDepth(t, b); d > cinderConfigMaxDepth {
				t.Fatalf("config nests %d levels with config as level 1, cinder drops more than %d", d, cinderConfigMaxDepth)
			}
			t.Logf("largest valid v2 config: %d B of %d", len(b), c.limit)
		})
	}
}

func TestKnobRoomBlockMergesFieldByField(t *testing.T) {
	cur := DefaultKnobSettings()
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"room":{"enabled":true,"presence_off_s":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	prev := got.clone()
	got, err = mergeKnobSettings(got.clone(), []byte(`{"room":{"moments":false,"presence_off_s":600}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := got.Room; !*r.Enabled || *r.PresenceOffS != 600 || *r.Moments || r.PresenceWake != nil {
		t.Fatalf("room patch lost fields: %+v", r)
	}
	if *prev.Room.PresenceOffS != 0 || prev.Room.Moments != nil {
		t.Fatalf("merging into a clone wrote through: %+v", prev.Room)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"room":{"moments":null}}`))
	if err != nil || got.Room.Moments != nil || !*got.Room.Enabled {
		t.Fatalf("null must clear one key: %v %+v", err, got.Room)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"room":null}`))
	if err != nil || got.Room != nil {
		t.Fatalf("null must clear the block: %v %+v", err, got.Room)
	}
}

func TestKnobRoomPageReachesAStoredConfigOffWithoutABump(t *testing.T) {
	const stored = `{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},` +
		`"pages":[{"id":"bot","on":true},{"id":"day","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":false},{"id":"controls","on":false},{"id":"pomodoro","on":false}],` +
		`"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20},"diagnostics":"off"}`
	var s KnobSettings
	if err := json.Unmarshal([]byte(stored), &s); err != nil {
		t.Fatal(err)
	}
	s.FillDefaults()
	if err := s.Validate(); err != nil {
		t.Fatalf("a config with no room entries must stay valid: %v", err)
	}
	if last := s.Pages[len(s.Pages)-1]; last != (KnobPage{ID: "room"}) {
		t.Fatalf("last page = %+v, want room off", last)
	}
	if s.Room != nil {
		t.Fatalf("a stored config must not gain a room block: %+v", s.Room)
	}
}

func TestKnobSwipePagesDefaultsOn(t *testing.T) {
	d := DefaultKnobSettings()
	if d.SwipePages == nil || !*d.SwipePages {
		t.Fatalf("default swipe_pages = %v, want on", d.SwipePages)
	}
}

func TestKnobSwipePagesStoredWithoutKeyReadsOn(t *testing.T) {
	var s KnobSettings
	if err := json.Unmarshal([]byte(`{"pages":[{"id":"bot","on":true}],"home":"bot","bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.FillDefaults()
	if s.SwipePages == nil || !*s.SwipePages {
		t.Fatal("a stored record without swipe_pages reads it on")
	}
}

func TestKnobSwipePagesCloneDoesNotShareThePointer(t *testing.T) {
	cur := DefaultKnobSettings()
	c := cur.clone()
	*c.SwipePages = false
	if !*cur.SwipePages {
		t.Fatal("clone shares swipe_pages with the original")
	}
}

func TestKnobSwipePagesMergeFalseAndNull(t *testing.T) {
	cur := DefaultKnobSettings()
	off, err := mergeKnobSettings(cur.clone(), []byte(`{"swipe_pages":false}`))
	if err != nil || off.SwipePages == nil || *off.SwipePages || !*cur.SwipePages {
		t.Fatalf("merge false: %v %v (original %v)", err, off.SwipePages, *cur.SwipePages)
	}
	kept, err := mergeKnobSettings(off.clone(), []byte(`{"poll_ms":3000}`))
	if err != nil || kept.SwipePages == nil || *kept.SwipePages {
		t.Fatalf("a partial PUT dropped swipe_pages false: %v %v", err, kept.SwipePages)
	}
	reset, err := mergeKnobSettings(off.clone(), []byte(`{"swipe_pages":null}`))
	if err != nil || reset.SwipePages == nil || !*reset.SwipePages {
		t.Fatalf("swipe_pages null = %v %v, want on", err, reset.SwipePages)
	}
}

func TestKnobWeatherMomentsDefaultOnEveryHalfHour(t *testing.T) {
	d := DefaultKnobSettings()
	if d.Bot.WeatherMoments == nil || !*d.Bot.WeatherMoments || d.Bot.WeatherEveryMin == nil || *d.Bot.WeatherEveryMin != 30 {
		t.Fatalf("defaults: %+v", d.Bot)
	}
	var s KnobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.FillDefaults()
	if s.Bot.WeatherMoments == nil || !*s.Bot.WeatherMoments || s.Bot.WeatherEveryMin == nil || *s.Bot.WeatherEveryMin != 30 {
		t.Fatalf("a stored record without them must read on, every 30 min: %+v", s.Bot)
	}
}

func TestKnobWeatherMomentsMergeKeepZeroAndNullReadsDefault(t *testing.T) {
	cur := DefaultKnobSettings()
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"bot":{"weather_every_min":0}}`))
	if err != nil || got.Bot.WeatherEveryMin == nil || *got.Bot.WeatherEveryMin != 0 || !*got.Bot.WeatherMoments {
		t.Fatalf("merge 0: %v %+v", err, got.Bot)
	}
	if *cur.Bot.WeatherEveryMin != 30 {
		t.Fatal("merging into a clone wrote through to the original")
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"bot":{"weather_moments":false}}`))
	if err != nil || *got.Bot.WeatherMoments || *got.Bot.WeatherEveryMin != 0 {
		t.Fatalf("merge off: %v %+v", err, got.Bot)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"bot":{"weather_moments":null,"weather_every_min":null}}`))
	if err != nil || !*got.Bot.WeatherMoments || *got.Bot.WeatherEveryMin != 30 {
		t.Fatalf("null must read as the defaults: %v %+v", err, got.Bot)
	}
}

func TestKnobDisplayFastLinkDefaultsOnAndMerges(t *testing.T) {
	var s KnobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.FillDefaults()
	if s.Display.FastLink == nil || !*s.Display.FastLink {
		t.Fatal("a stored record without display reads fast_link on")
	}
	cur := DefaultKnobSettings()
	off, err := mergeKnobSettings(cur.clone(), []byte(`{"display":{"fast_link":false}}`))
	if err != nil || *off.Display.FastLink || !*cur.Display.FastLink {
		t.Fatalf("merge: %v %+v (original %+v)", err, off.Display, cur.Display)
	}
	if _, err := mergeKnobSettings(cur.clone(), []byte(`{"display":{"turbo":true}}`)); err == nil {
		t.Fatal("unknown display field accepted")
	}
}

func TestKnobBotFlagsDefaultOnAndMerge(t *testing.T) {
	var s KnobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.FillDefaults()
	if s.Bot.SourceLabel == nil || !*s.Bot.SourceLabel || s.Bot.WorkingRing == nil || !*s.Bot.WorkingRing {
		t.Fatalf("a stored record without the flags must read as on: %+v", s.Bot)
	}
	cur := DefaultKnobSettings()
	off, err := mergeKnobSettings(cur.clone(), []byte(`{"bot":{"working_ring":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if *off.Bot.WorkingRing || !*off.Bot.SourceLabel {
		t.Fatalf("merge: %+v", off.Bot)
	}
	if !*cur.Bot.WorkingRing {
		t.Fatal("merging into a clone wrote through to the original")
	}
	null, err := mergeKnobSettings(off.clone(), []byte(`{"bot":{"working_ring":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if null.Bot.WorkingRing == nil || !*null.Bot.WorkingRing {
		t.Fatalf("null must read as on: %+v", null.Bot)
	}
}
