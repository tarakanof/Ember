package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestKnobV2KeysValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*knobSettings)
		ok     bool
	}{
		{"dim never", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(0)} }, true},
		{"dim 30", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(30)} }, true},
		{"dim 29", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(29)} }, false},
		{"dim 86400", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(86400)} }, true},
		{"dim 86401", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(86401)} }, false},
		{"dim negative", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(-1)} }, false},
		{"level 0", func(s *knobSettings) { s.Idle = &knobIdle{DimLevel: new(0)} }, false},
		{"level 1", func(s *knobSettings) { s.Idle = &knobIdle{DimLevel: new(1)} }, true},
		{"level 255", func(s *knobSettings) { s.Idle = &knobIdle{DimLevel: new(255)} }, true},
		{"level 256", func(s *knobSettings) { s.Idle = &knobIdle{DimLevel: new(256)} }, false},
		{"off never", func(s *knobSettings) { s.Idle = &knobIdle{OffAfterS: new(0)} }, true},
		{"off 59", func(s *knobSettings) { s.Idle = &knobIdle{OffAfterS: new(59)} }, false},
		{"off 60", func(s *knobSettings) { s.Idle = &knobIdle{OffAfterS: new(60)} }, true},
		{"off 86401", func(s *knobSettings) { s.Idle = &knobIdle{OffAfterS: new(86401)} }, false},
		{"off after dim", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(600), OffAfterS: new(601)} }, true},
		{"off equals dim", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(600), OffAfterS: new(600)} }, false},
		{"off before dim", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(600), OffAfterS: new(300)} }, false},
		{"off with dim never", func(s *knobSettings) { s.Idle = &knobIdle{DimAfterS: new(0), OffAfterS: new(60)} }, true},
		{"colour upper", func(s *knobSettings) { s.Apps = &knobApps{Pomodoro: &knobPomoApp{FocusColor: new("#FF6A3D")}} }, true},
		{"colour lower", func(s *knobSettings) { s.Apps = &knobApps{Pomodoro: &knobPomoApp{BreakColor: new("#4fa9ff")}} }, true},
		{"colour no hash", func(s *knobSettings) { s.Apps = &knobApps{Pomodoro: &knobPomoApp{FocusColor: new("FF6A3D")}} }, false},
		{"colour short", func(s *knobSettings) { s.Apps = &knobApps{Pomodoro: &knobPomoApp{FocusColor: new("#FFF")}} }, false},
		{"colour not hex", func(s *knobSettings) { s.Apps = &knobApps{Pomodoro: &knobPomoApp{FocusColor: new("#GGGGGG")}} }, false},
		{"colour alpha", func(s *knobSettings) { s.Apps = &knobApps{Pomodoro: &knobPomoApp{FocusColor: new("#FF6A3DFF")}} }, false},
		{"faces one", func(s *knobSettings) { s.Apps = &knobApps{Weather: &knobWeatherApp{Faces: []string{"sun"}}} }, true},
		{"faces all", func(s *knobSettings) { s.Apps = &knobApps{Weather: &knobWeatherApp{Faces: knobWeatherFaces}} }, true},
		{"faces empty", func(s *knobSettings) { s.Apps = &knobApps{Weather: &knobWeatherApp{Faces: []string{}}} }, false},
		{"faces unknown", func(s *knobSettings) { s.Apps = &knobApps{Weather: &knobWeatherApp{Faces: []string{"snow"}}} }, false},
		{"faces duplicate", func(s *knobSettings) { s.Apps = &knobApps{Weather: &knobWeatherApp{Faces: []string{"sun", "sun"}}} }, false},
		{"faces eight", func(s *knobSettings) {
			s.Apps = &knobApps{Weather: &knobWeatherApp{Faces: append([]string{"sun"}, knobWeatherFaces...)}}
		}, false},
		{"np dim 59", func(s *knobSettings) { s.Apps = &knobApps{NowPlaying: &knobNPApp{DimAfterS: new(59)}} }, false},
		{"np dim 60", func(s *knobSettings) { s.Apps = &knobApps{NowPlaying: &knobNPApp{DimAfterS: new(60)}} }, true},
		{"np dim 1800", func(s *knobSettings) { s.Apps = &knobApps{NowPlaying: &knobNPApp{DimAfterS: new(1800)}} }, true},
		{"np dim 1801", func(s *knobSettings) { s.Apps = &knobApps{NowPlaying: &knobNPApp{DimAfterS: new(1801)}} }, false},
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

func TestKnobV2RejectsUnknownKeysAndPages(t *testing.T) {
	for _, patch := range []string{
		`{"idle":{"sleep_after_s":5}}`,
		`{"apps":{"bot":{"x":1}}}`,
		`{"apps":{"pomodoro":{"ring_color":"#FFFFFF"}}}`,
		`{"apps":{"weather":{"units":"c"}}}`,
		`{"apps":{"nowplaying":{"brightness":5}}}`,
	} {
		if _, err := mergeKnobSettings(defaultKnobSettings(), []byte(patch)); err == nil {
			t.Errorf("%s accepted", patch)
		}
	}
}

func TestKnobV2MergeFieldByField(t *testing.T) {
	cur := defaultKnobSettings()
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

const preV2StoredConfig = `{"brightness":{"follow_ember":false,"level":200,"floor":20,"startup":120},` +
	`"pages":[{"id":"pomodoro","on":true},{"id":"bot","on":true},{"id":"nowplaying","on":true},{"id":"weather","on":false},{"id":"future-page","on":false}],` +
	`"home":"pomodoro","swipe_pages":false,"poll_ms":3000,` +
	`"bot":{"sleepy_after_s":600,"demo_hold_s":30,"source_label":false,"working_ring":false,"weather_moments":false,"weather_every_min":0},` +
	`"diagnostics":"basic","stats_interval_s":120,"live_interval_s":2,"display":{"fast_link":false,"rotation":180},"quiet":{"calm":false,"dim_level":5}}`

func TestKnobV2AbsentKeepsLegacyBytes(t *testing.T) {
	var back knobSettings
	if err := json.Unmarshal([]byte(preV2StoredConfig), &back); err != nil {
		t.Fatal(err)
	}
	back.fillDefaults()
	again, _ := json.Marshal(back)
	if string(again) != preV2StoredConfig {
		t.Fatalf("load/save changed the bytes\n got %s\nwant %s", again, preV2StoredConfig)
	}
	f := newViewFixture(t)
	putKnobConfig(t, f.srv, f.m.ID, preV2StoredConfig)
	for _, patch := range []string{`{}`, `{"idle":{}}`, `{"idle":null}`, `{"apps":{}}`, `{"apps":{"pomodoro":{}}}`,
		`{"apps":{"weather":{"faces":null}}}`, `{"apps":{"pomodoro":{"focus_color":null,"break_color":null}}}`} {
		_, v0, _ := f.app.devices.config(f.m.ID)
		resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, patch)
		mustOK(t, patch, resp, b)
		cfg, v1, _ := f.app.devices.config(f.m.ID)
		if v1 != v0 {
			t.Fatalf("%s: config_version %d -> %d", patch, v0, v1)
		}
		if now, _ := json.Marshal(cfg); string(now) != preV2StoredConfig {
			t.Fatalf("%s: stored config changed\n got %s\nwant %s", patch, now, preV2StoredConfig)
		}
	}
}

func TestKnobV2ClearingTheLastFieldReturnsToAbsent(t *testing.T) {
	cur := defaultKnobSettings()
	legacy, _ := json.Marshal(cur)
	got, err := mergeKnobSettings(cur.clone(), []byte(`{"idle":{"dim_level":40},"apps":{"pomodoro":{"focus_color":"#aabbcc"},"weather":{"faces":["sun"]},"nowplaying":{"dim_after_s":60}}}`))
	if err != nil || got.Idle == nil || got.Apps == nil {
		t.Fatalf("set: %v %+v", err, got)
	}
	got, err = mergeKnobSettings(got.clone(), []byte(`{"idle":{"dim_level":null},"apps":{"pomodoro":{"focus_color":null},"weather":{"faces":null},"nowplaying":{"dim_after_s":null}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if now, _ := json.Marshal(got); string(now) != string(legacy) {
		t.Fatalf("clearing every field left residue\n got %s\nwant %s", now, legacy)
	}
}

func maxV2Settings() knobSettings {
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
	s.Bot.WeatherMoments, s.Bot.WeatherEveryMin = boolPtr(false), new(240)
	s.Display.FastLink, s.Quiet.Calm, s.Quiet.DimLevel = boolPtr(false), boolPtr(false), new(255)
	s.Display.Rotation = 270
	s.SwipePages = boolPtr(false)
	s.Idle = &knobIdle{DimAfterS: new(86399), DimLevel: new(255), OffAfterS: new(86400)}
	s.Apps = &knobApps{
		Pomodoro:   &knobPomoApp{FocusColor: new("#FFFFFF"), BreakColor: new("#FFFFFF")},
		Weather:    &knobWeatherApp{Faces: slices.Clone(knobWeatherFaces)},
		NowPlaying: &knobNPApp{DimAfterS: new(1800)},
	}
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

func TestConfigDepthCountsConfigAsLevelOne(t *testing.T) {
	if d := configDepth(t, []byte(`{"a":[{"b":{}},1]}`)); d != cinderConfigMaxDepth {
		t.Fatalf("cinder's kept fixture has depth %d, want %d", d, cinderConfigMaxDepth)
	}
	if d := configDepth(t, []byte(`{"a":[{"b":{"c":1}}],"apps":{"weather":{"faces":[{"x":1}]}}}`)); d <= cinderConfigMaxDepth {
		t.Fatalf("a 5-level config has depth %d, want over %d", d, cinderConfigMaxDepth)
	}
}

func TestKnobV2LargestConfigFitsDepthAndSize(t *testing.T) {
	s := maxV2Settings()
	if err := s.validate(); err != nil {
		t.Fatalf("the largest v2 config is rejected: %v", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > legacyCfgSettingsMax {
		t.Fatalf("the largest valid v2 config is %d B, over the knob's %d", len(b), legacyCfgSettingsMax)
	}
	if d := configDepth(t, b); d > cinderConfigMaxDepth {
		t.Fatalf("config nests %d levels with config as level 1, cinder drops more than %d", d, cinderConfigMaxDepth)
	}
	t.Logf("largest valid v2 config: %d B of %d", len(b), legacyCfgSettingsMax)
}

func TestCapsKnobV2OverConfigBytes400(t *testing.T) {
	put := func(f *viewFixture, body string) (*http.Response, []byte) {
		return devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, body)
	}
	pages := []string{"bot", "pomodoro", "weather"}

	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": append([]string{"page-00000000000"}, pages...),
		"rotations": []int{0, 90, 180, 270}, "limits": map[string]any{"config_bytes": legacyCfgSettingsMax}})
	big, _ := json.Marshal(maxV2Settings())
	resp, b := put(f, string(big))
	mustOK(t, "the largest v2 config on a 1024 knob", resp, b)

	f = newViewFixture(t)
	cfg, _, _ := f.app.devices.config(f.m.ID)
	cur, _ := json.Marshal(cfg)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": pages,
		"limits": map[string]any{"config_bytes": len(cur) + 40}})
	resp, b = put(f, `{"idle":{"dim_after_s":600}}`)
	mustOK(t, "a v2 key that still fits", resp, b)
	resp, b = put(f, `{"idle":{"dim_level":40,"off_after_s":1800},"apps":{"weather":{"faces":["sun","temp","rain","cast"]}}}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "config_bytes") {
		t.Fatalf("a PUT that grows past config_bytes = %d %s, want 400 naming config_bytes", resp.StatusCode, b)
	}
}
