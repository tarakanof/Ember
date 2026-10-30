package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tarakanof/ember/internal/devices"
)

const preV2StoredConfig = `{"brightness":{"follow_ember":false,"level":200,"floor":20,"startup":120},` +
	`"pages":[{"id":"pomodoro","on":true},{"id":"bot","on":true},{"id":"nowplaying","on":true},{"id":"weather","on":false},{"id":"future-page","on":false},{"id":"day","on":false},{"id":"room","on":false},{"id":"controls","on":false}],` +
	`"home":"pomodoro","swipe_pages":false,"poll_ms":3000,` +
	`"bot":{"sleepy_after_s":600,"demo_hold_s":30,"source_label":false,"working_ring":false,"weather_moments":false,"weather_every_min":0},` +
	`"diagnostics":"basic","stats_interval_s":120,"live_interval_s":2,"display":{"fast_link":false,"rotation":180},"quiet":{"calm":false,"dim_level":5}}`

func TestKnobV2AbsentKeepsLegacyBytes(t *testing.T) {
	var back devices.KnobSettings
	if err := json.Unmarshal([]byte(preV2StoredConfig), &back); err != nil {
		t.Fatal(err)
	}
	back.FillDefaults()
	again, _ := json.Marshal(back)
	if string(again) != preV2StoredConfig {
		t.Fatalf("load/save changed the bytes\n got %s\nwant %s", again, preV2StoredConfig)
	}
	f := newViewFixture(t)
	putKnobConfig(t, f.srv, f.m.ID, preV2StoredConfig)
	for _, patch := range []string{`{}`, `{"idle":{}}`, `{"idle":null}`, `{"apps":{}}`, `{"apps":{"pomodoro":{}}}`,
		`{"apps":{"weather":{"faces":null}}}`, `{"apps":{"pomodoro":{"focus_color":null,"break_color":null}}}`,
		`{"room":{}}`, `{"room":null}`, `{"room":{"enabled":null,"moments":null}}`} {
		_, v0, _ := f.app.devices.Config(f.m.ID)
		resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, patch)
		mustOK(t, patch, resp, b)
		cfg, v1, _ := f.app.devices.Config(f.m.ID)
		if v1 != v0 {
			t.Fatalf("%s: config_version %d -> %d", patch, v0, v1)
		}
		if now, _ := json.Marshal(cfg); string(now) != preV2StoredConfig {
			t.Fatalf("%s: stored config changed\n got %s\nwant %s", patch, now, preV2StoredConfig)
		}
	}
}

func maxLegacyStoreSettings() devices.KnobSettings {
	s := devices.DefaultKnobSettings()
	s.Pages = nil
	for i := range devices.KnobMaxPages {
		s.Pages = append(s.Pages, devices.KnobPage{ID: fmt.Sprintf("page-%011d", i), On: false})
	}
	s.Pages[0].On, s.Home = true, s.Pages[0].ID
	s.Brightness = devices.KnobBrightness{FollowEmber: false, Level: 255, Floor: 255, Startup: 255}
	s.PollMS, s.Diagnostics, s.StatsIntervalS, s.LiveIntervalS = 10000, devices.KnobDiagBasic, 300, 10
	s.Bot.SleepyAfterS, s.Bot.DemoHoldS = 86400, 600
	s.Bot.SourceLabel, s.Bot.WorkingRing = boolPtr(false), boolPtr(false)
	s.Bot.WeatherMoments, s.Bot.WeatherEveryMin = boolPtr(false), new(240)
	s.Display.FastLink, s.Quiet.Calm, s.Quiet.DimLevel = boolPtr(false), boolPtr(false), new(255)
	s.Display.Rotation = 270
	s.SwipePages = boolPtr(false)
	s.Idle = &devices.KnobIdle{DimAfterS: new(86399), DimLevel: new(255), OffAfterS: new(86400)}
	s.Apps = &devices.KnobApps{
		Pomodoro:   &devices.KnobPomoApp{FocusColor: new("#FFFFFF"), BreakColor: new("#FFFFFF")},
		Weather:    &devices.KnobWeatherApp{Faces: slices.Clone(devices.KnobWeatherFaces)},
		NowPlaying: &devices.KnobNPApp{DimAfterS: new(1800)},
	}
	return s
}

func maxV2Settings() devices.KnobSettings {
	s := maxLegacyStoreSettings()
	s.Room = &devices.KnobRoom{Enabled: boolPtr(false), PresenceOffS: new(86399), PresenceWake: boolPtr(false), Moments: boolPtr(false)}
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

func TestCapsKnobV2OverConfigBytes400(t *testing.T) {
	put := func(f *viewFixture, body string) (*http.Response, []byte) {
		return devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, body)
	}
	pages := []string{"bot", "pomodoro", "weather"}

	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": append([]string{"page-00000000000"}, pages...),
		"rotations": []int{0, 90, 180, 270}, "limits": map[string]any{"config_bytes": legacyCfgSettingsMax}})
	big, _ := json.Marshal(maxLegacyStoreSettings())
	resp, b := put(f, string(big))
	mustOK(t, "the largest v2 config on a 1024 knob", resp, b)
	resp, b = put(f, `{"room":{"enabled":false,"presence_off_s":86399,"presence_wake":false,"moments":false}}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "config_bytes") {
		t.Fatalf("the room block on a full 1024 knob = %d %s, want 400 naming config_bytes", resp.StatusCode, b)
	}

	f = newViewFixture(t)
	cfg, _, _ := f.app.devices.Config(f.m.ID)
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

func TestKnobRoomConfigRoundTripsThroughTheOwnerAndTheKnob(t *testing.T) {
	f := newViewFixture(t)
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken,
		`{"pages":[{"id":"bot","on":true},{"id":"room","on":true}],"room":{"enabled":true,"presence_off_s":120}}`)
	mustOK(t, "put room", resp, b)
	check := func(where string, c devices.KnobSettings) {
		t.Helper()
		if !c.PageOn("room") || !*c.Room.Enabled || *c.Room.PresenceOffS != 120 {
			t.Fatalf("%s lost the room settings: %+v", where, c)
		}
	}
	resp, b = devReq(t, f.srv, "GET", "/v1/devices/"+f.m.ID+"/config", testToken, "")
	var owner devices.KnobSettings
	if err := json.Unmarshal(mustOK(t, "owner get", resp, b), &owner); err != nil {
		t.Fatal(err)
	}
	check("owner get", owner)
	resp, b = devReq(t, f.srv, "GET", "/v1/devices/self/config", f.m.Token, "")
	var self struct {
		Config devices.KnobSettings `json:"config"`
	}
	if err := json.Unmarshal(mustOK(t, "self get", resp, b), &self); err != nil {
		t.Fatal(err)
	}
	check("self get", self.Config)
}

func TestKnobRoomPresenceOffOutOfRangeIs400(t *testing.T) {
	f := newViewFixture(t)
	for _, v := range []string{"-1", "86400"} {
		resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"room":{"presence_off_s":`+v+`}}`)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "room.presence_off_s") {
			t.Fatalf("presence_off_s %s = %d %s, want 400 naming the key", v, resp.StatusCode, b)
		}
	}
}

func TestCapsKnobCannotTurnOnTheRoomPageOutsideItsCaps(t *testing.T) {
	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"}})
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"pages":[{"id":"bot","on":true},{"id":"room","on":true}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("room page outside caps = %d %s, want 400", resp.StatusCode, b)
	}
}
