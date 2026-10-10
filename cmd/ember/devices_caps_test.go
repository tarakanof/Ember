package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var knobCapsFull = map[string]any{
	"view":      []int{1, 1},
	"pages":     []string{"bot", "pomodoro", "weather", "nowplaying"},
	"features":  []string{"view_wait", "np_control", "ota_rollback", "coredump", "stats_intervals"},
	"limits":    map[string]any{"view_bytes": 16383, "config_bytes": 1024},
	"rotations": []int{0, 180},
}

func capsCheckinBody(t *testing.T, fw string, version int, caps any) string {
	t.Helper()
	body := map[string]any{
		"config_version": version, "fw": fw, "heap_internal_free": 47104, "heap_internal_largest": 31744,
		"ip": "192.0.2.10", "rssi": -58, "uptime_s": 812,
	}
	if caps != nil {
		body["caps"] = caps
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func postCapsCheckin(t *testing.T, f *viewFixture, fw string, caps any) map[string]any {
	t.Helper()
	_, version, err := f.app.devices.versions(f.m.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, capsCheckinBody(t, fw, version, caps))
	mustOK(t, "checkin", resp, b)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func viewKeys(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func knobEffectiveCaps(t *testing.T, f *viewFixture) map[string]any {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/devices", testToken, "")
	mustOK(t, "list", resp, b)
	var out struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Devices {
		if d["id"] == f.m.ID {
			c, _ := d["effective_caps"].(map[string]any)
			return c
		}
	}
	t.Fatalf("knob %s not listed: %s", f.m.ID, b)
	return nil
}

func TestLegacyCapsTable(t *testing.T) {
	base := []string{"bot", "pomodoro", "weather"}
	withNP := []string{"bot", "pomodoro", "weather", "nowplaying"}
	limits := &deviceCapsLimits{ViewBytes: 16383, ConfigBytes: 1024}
	cases := []struct {
		fw       string
		pages    []string
		features []string
		limits   *deviceCapsLimits
	}{
		{"", base, []string{}, nil},
		{"dev", base, []string{}, nil},
		{"0.9", base, []string{}, nil},
		{"0.6.9", base, []string{}, nil},
		{"0.7.0", base, []string{"stats_intervals"}, nil},
		{"0.8.0", base, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.0-rc.1", base, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.0", withNP, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.5", withNP, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.6", withNP, []string{"stats_intervals", "view_wait", "np_control"}, nil},
		{"0.9.13", withNP, []string{"stats_intervals", "view_wait", "np_control"}, nil},
		{"0.9.14", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump"}, nil},
		{"0.9.16", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, nil},
		{"0.9.27", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, nil},
		{"0.9.28", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.9.41", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.10.0", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.9.41+local", withNP, []string{"stats_intervals", "view_wait", "np_control", "coredump", "ota_rollback"}, limits},
		{"0.9.0+build.7", withNP, []string{"stats_intervals", "view_wait"}, nil},
		{"0.9.0-rc.1+build.7", base, []string{"stats_intervals", "view_wait"}, nil},
	}
	for _, c := range cases {
		t.Run(c.fw, func(t *testing.T) {
			got := legacyCaps(c.fw)
			want := deviceCaps{View: []int{1, 1}, Pages: c.pages, Features: c.features, Limits: c.limits}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("legacyCaps(%q) = %+v, want %+v", c.fw, got, want)
			}
			if err := got.validate(); err != nil {
				t.Errorf("legacy caps for %q do not validate: %v", c.fw, err)
			}
		})
	}
}

func TestLegacyCapsTableIsOrderedAndUsesKnownTokens(t *testing.T) {
	known := []string{featureViewWait, featureNPControl, featureOTARollback, featureCoredump, featureStatsIntervals}
	for i, row := range legacyKnobCaps {
		if !semverPattern.MatchString(row.minFW) {
			t.Errorf("row %d: %q is not a version", i, row.minFW)
		}
		if i > 0 && compareSemver(legacyKnobCaps[i-1].minFW, row.minFW) >= 0 {
			t.Errorf("row %d (%s) is not after row %d (%s)", i, row.minFW, i-1, legacyKnobCaps[i-1].minFW)
		}
		for _, f := range row.features {
			if !slices.Contains(known, f) {
				t.Errorf("row %s: unknown feature token %q", row.minFW, f)
			}
		}
		for _, p := range row.pages {
			if !slices.Contains(knobPageIDs, p) {
				t.Errorf("row %s: unknown page %q", row.minFW, p)
			}
		}
	}
}

var versionLiteral = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

func TestVersionLiteralsOnlyInTheLegacyCapsTable(t *testing.T) {
	var files []string
	for _, root := range []string{".", filepath.Join("..", "..", "internal")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && versionLiteral.MatchString(s) && path != "devices_caps.go" {
				t.Errorf("%s: version literal %q; behaviour keys on caps, and only the legacy caps table may name a firmware version", fset.Position(lit.Pos()), s)
			}
			return true
		})
	}
}

func TestLegacyFirmwareViewIsUnchanged(t *testing.T) {
	for _, fw := range []string{"0.8.3", "0.9.41"} {
		t.Run(fw, func(t *testing.T) {
			f := goldenViewFixture(t)
			fullViewScenario(t, f)
			before, etag, err := f.app.knobView(f.m.ID, f.clk.Now())
			if err != nil {
				t.Fatal(err)
			}
			reply := postCapsCheckin(t, f, fw, nil)
			if _, ok := reply["caps_ack"]; ok {
				t.Errorf("a checkin without caps got caps_ack: %v", reply)
			}
			after, etag2, err := f.app.knobView(f.m.ID, f.clk.Now())
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) || etag2 != etag {
				t.Fatalf("legacy view changed after a %s checkin:\n%s\n%s", fw, before, after)
			}
			assertDeviceGolden(t, "view_full", after)
			if c := knobEffectiveCaps(t, f); c["source"] != capsSourceLegacy {
				t.Errorf("effective_caps = %v, want source legacy", c)
			}
		})
	}
}

func TestCapsKnobViewIsFilteredByCapsAndPages(t *testing.T) {
	f := goldenViewFixture(t)
	fullViewScenario(t, f)
	caps := map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "weather"}, "features": []string{"view_wait"},
		"limits": map[string]any{"view_bytes": 16383, "config_bytes": 1024}}
	reply := postCapsCheckin(t, f, "0.10.0", caps)
	if reply["caps_ack"] != true {
		t.Fatalf("reply = %v, want caps_ack true", reply)
	}
	body := goldenView(t, f)
	assertDeviceGolden(t, "view_caps_limited", body)
	keys := viewKeys(t, body)
	for _, k := range []string{"mood", "weather", "brightness", "diag_live_until"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("view lacks %q: %s", k, body)
		}
	}
	for _, k := range []string{"pomo", "nowplaying"} {
		if _, ok := keys[k]; ok {
			t.Errorf("view carries %q, a page outside caps: %s", k, body)
		}
	}

	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"weather","on":false}]}`)
	keys = viewKeys(t, goldenView(t, f))
	if _, ok := keys["weather"]; ok {
		t.Errorf("weather block sent with the weather page off")
	}
	if _, ok := keys["mood"]; !ok {
		t.Errorf("mood block missing with the bot page on")
	}
}

func TestCapsKnobViewMajorIsTheLowerOfServerAndFirmware(t *testing.T) {
	for _, view := range [][]int{{1, 1}, {1, 5}, {2, 3}} {
		f := newViewFixture(t)
		postCapsCheckin(t, f, "0.10.0", map[string]any{"view": view, "pages": []string{"bot"}})
		var v struct {
			V int `json:"v"`
		}
		if err := json.Unmarshal(goldenView(t, f), &v); err != nil {
			t.Fatal(err)
		}
		if v.V != min(knobViewMajorMax, view[1]) {
			t.Errorf("caps.view %v: v = %d, want %d", view, v.V, min(knobViewMajorMax, view[1]))
		}
	}
}

func plantCaps(t *testing.T, f *viewFixture, caps *deviceCaps) {
	t.Helper()
	f.app.devices.mu.Lock()
	defer f.app.devices.mu.Unlock()
	f.app.devices.state.findKnob(f.m.ID).Caps = caps
}

func TestCapsKnobViewDropsBlocksToFitViewBytes(t *testing.T) {
	f := goldenViewFixture(t)
	fullViewScenario(t, f)
	postCapsCheckin(t, f, "0.10.0", knobCapsFull)
	full := goldenView(t, f)
	keys := viewKeys(t, full)
	for _, k := range []string{"mood", "pomo", "weather", "nowplaying"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("full caps view lacks %q: %s", k, full)
		}
	}
	pages := []string{"bot", "pomodoro", "weather", "nowplaying"}
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: pages, Features: []string{}, Limits: &deviceCapsLimits{ViewBytes: len(full) - 1}})
	body := goldenView(t, f)
	if len(body) > len(full)-1 {
		t.Fatalf("view is %d B, over the %d B limit", len(body), len(full)-1)
	}
	keys = viewKeys(t, body)
	if _, ok := keys["nowplaying"]; ok {
		t.Errorf("nowplaying kept in an oversized view")
	}
	if _, ok := keys["pomo"]; !ok {
		t.Errorf("pomo dropped though dropping nowplaying was enough")
	}
}

func TestCapsKnobViewStillOverViewBytesLogsOncePerDevice(t *testing.T) {
	f := goldenViewFixture(t)
	fullViewScenario(t, f)
	var logs syncBuffer
	f.app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	pages := []string{"bot", "pomodoro", "weather", "nowplaying"}
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: pages, Features: []string{}, Limits: &deviceCapsLimits{ViewBytes: 10}})
	for range 3 {
		body := goldenView(t, f)
		keys := viewKeys(t, body)
		if _, ok := keys["mood"]; !ok || len(keys["pomo"]) > 0 || len(keys["weather"]) > 0 || len(keys["nowplaying"]) > 0 {
			t.Fatalf("trimmed view = %s, want mood kept and the rest dropped", body)
		}
	}
	if n := strings.Count(logs.String(), "knob view over its caps view_bytes"); n != 1 {
		t.Fatalf("over-limit logs = %d, want 1:\n%s", n, logs.String())
	}
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: pages, Features: []string{}})
	goldenView(t, f)
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: pages, Features: []string{}, Limits: &deviceCapsLimits{ViewBytes: 10}})
	goldenView(t, f)
	if n := strings.Count(logs.String(), "knob view over its caps view_bytes"); n != 2 {
		t.Fatalf("over-limit logs after it fit and broke again = %d, want 2", n)
	}
}

func TestCapsLimitFloors(t *testing.T) {
	floor := defaultKnobConfigBytes()
	cases := []struct {
		limits map[string]any
		ok     bool
	}{
		{map[string]any{"view_bytes": capsMinViewBytes - 1}, false},
		{map[string]any{"view_bytes": capsMinViewBytes}, true},
		{map[string]any{"view_bytes": 1}, false},
		{map[string]any{"config_bytes": floor - 1}, false},
		{map[string]any{"config_bytes": floor}, true},
		{map[string]any{"view_bytes": 0, "config_bytes": 0}, true},
	}
	for _, c := range cases {
		f := newViewFixture(t)
		reply := postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "limits": c.limits})
		if got := reply["caps_ack"] == true; got != c.ok {
			t.Errorf("limits %v: acked = %v, want %v", c.limits, got, c.ok)
		}
	}
	if floor < 300 || floor > cinderCfgSettingsMax {
		t.Errorf("default config floor = %d B, want the default config's size under the knob's store", floor)
	}
}

func TestLegacyKnobAlwaysGetsMood(t *testing.T) {
	f := newViewFixture(t)
	putKnobConfig(t, f.srv, f.m.ID, `{"home":"pomodoro","pages":[{"id":"bot","on":false},{"id":"pomodoro","on":true}]}`)
	for _, fw := range []string{"", "0.9.41"} {
		if fw != "" {
			postCapsCheckin(t, f, fw, nil)
		}
		if _, ok := viewKeys(t, goldenView(t, f))["mood"]; !ok {
			t.Fatalf("fw %q: a caps-less knob with the bot page off got no mood", fw)
		}
	}
}

func TestReorderedCapsDoNotWrite(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	kv, _ := countDeviceWrites(t, app)
	m := mintKnob(t, srv, http.StatusCreated)
	post := func(pages, features []string) map[string]any {
		t.Helper()
		resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token,
			capsCheckinBody(t, "0.10.0", 1, map[string]any{"view": []int{1, 1}, "pages": pages, "features": features}))
		mustOK(t, "checkin", resp, b)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	post([]string{"bot", "weather"}, []string{"view_wait", "coredump"})
	base := kv.puts.Load()
	for range 3 {
		post([]string{"weather", "bot"}, []string{"coredump", "view_wait"})
		post([]string{"bot", "weather"}, []string{"view_wait", "coredump"})
	}
	if got := kv.puts.Load() - base; got != 0 {
		t.Fatalf("store writes for reordered caps = %d, want 0", got)
	}
	c := app.devices.list()[0].EffectiveCaps
	if !slices.Equal(c.Pages, []string{"bot", "weather"}) || !slices.Equal(c.Features, []string{"coredump", "view_wait"}) {
		t.Fatalf("effective_caps = %+v, want the first page order and sorted features", c)
	}
}

func TestInvalidCapsAreExposedOnceWithoutRewrites(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	var logs syncBuffer
	app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	kv, _ := countDeviceWrites(t, app)
	m := mintKnob(t, srv, http.StatusCreated)
	post := func(caps any) {
		t.Helper()
		resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, capsCheckinBody(t, "0.9.41", 1, caps))
		mustOK(t, "checkin", resp, b)
	}
	bad := map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "bot"}}
	base := kv.puts.Load()
	for range 3 {
		post(bad)
	}
	if got := kv.puts.Load() - base; got != 1 {
		t.Fatalf("store writes for the same invalid caps = %d, want 1", got)
	}
	if n := strings.Count(logs.String(), "level=WARN msg=\"device caps dropped\""); n != 1 {
		t.Fatalf("caps drop warnings = %d, want 1:\n%s", n, logs.String())
	}
	c := app.devices.list()[0].EffectiveCaps
	if c.Source != capsSourceLegacy || !strings.Contains(c.CapsError, `page "bot" listed twice`) {
		t.Fatalf("effective_caps = %+v, want legacy with caps_error", c)
	}
	post(knobCapsFull)
	if c := app.devices.list()[0].EffectiveCaps; c.CapsError != "" || c.Source != capsSourceReported {
		t.Fatalf("effective_caps after good caps = %+v", c)
	}
	post(nil)
	if c := app.devices.list()[0].EffectiveCaps; c.CapsError != "" || c.Source != capsSourceLegacy {
		t.Fatalf("effective_caps without caps = %+v, want legacy and no error", c)
	}
}

func TestCapsKnobMayShrinkAConfigAlreadyOverConfigBytes(t *testing.T) {
	f := newViewFixture(t)
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"page-0000000001","on":false},{"id":"page-0000000002","on":false}]}`)
	cfg, _, _ := f.app.devices.config(f.m.ID)
	cur, _ := json.Marshal(cfg)
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: []string{"bot", "pomodoro", "weather"}, Features: []string{},
		Limits: &deviceCapsLimits{ConfigBytes: len(cur) - 100}})
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"page-0000000001","on":false}]}`)
	cfg, _, _ = f.app.devices.config(f.m.ID)
	if shrunk, _ := json.Marshal(cfg); len(shrunk) <= len(cur)-100 {
		t.Fatalf("shrunk config is %d B, want it still over the %d B limit", len(shrunk), len(cur)-100)
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"poll_ms":3000}`)
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"pages":[{"id":"bot","on":true},{"id":"page-0000000001","on":false},{"id":"page-0000000003","on":false}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("growing an over-limit config = %d %s, want 400", resp.StatusCode, b)
	}
}

func TestCapsKnobConfigPutRejectsPagesOutsideCaps(t *testing.T) {
	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"}})
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken,
		`{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":true}]}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), `page \"nowplaying\" is not in this knob's firmware`) {
		t.Fatalf("PUT a page outside caps = %d %s, want 400 naming it", resp.StatusCode, b)
	}
	resp, b = devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken,
		`{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":false},{"id":"future-page","on":false}]}`)
	mustOK(t, "PUT with pages outside caps left off", resp, b)
	putKnobConfig(t, f.srv, f.m.ID, `{"poll_ms":3000}`)
}

func TestCapsKnobKeepsAPageThatWasAlreadyOn(t *testing.T) {
	f := newViewFixture(t)
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":true}]}`)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}})
	putKnobConfig(t, f.srv, f.m.ID, `{"poll_ms":3000}`)
	if _, ok := viewKeys(t, goldenView(t, f))["nowplaying"]; ok {
		t.Errorf("view sent nowplaying to a knob whose caps lack it")
	}
}

func TestCapsKnobConfigPutChecksConfigBytes(t *testing.T) {
	f := newViewFixture(t)
	cfg, _, err := f.app.devices.config(f.m.ID)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := json.Marshal(cfg)
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"},
		"limits": map[string]any{"config_bytes": len(cur) + 2}})
	putKnobConfig(t, f.srv, f.m.ID, `{"poll_ms":3000}`)
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"pages":[{"id":"bot","on":true},{"id":"page-0000000001","on":false}]}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "config_bytes") {
		t.Fatalf("oversized config PUT = %d %s, want 400 naming config_bytes", resp.StatusCode, b)
	}
}

func TestLegacyKnobConfigPutIsNotCheckedAgainstTheTable(t *testing.T) {
	for _, fw := range []string{"", "0.6.0", "0.9.41"} {
		f := newViewFixture(t)
		if fw != "" {
			postCapsCheckin(t, f, fw, nil)
		}
		putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true},{"id":"future-page","on":true}]}`)
	}
}

func TestCapsAreStoredExposedAndClearedOnDowngrade(t *testing.T) {
	f := newViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", knobCapsFull)
	c := knobEffectiveCaps(t, f)
	if c["source"] != capsSourceReported || !reflect.DeepEqual(c["pages"], []any{"bot", "pomodoro", "weather", "nowplaying"}) ||
		!reflect.DeepEqual(c["limits"], map[string]any{"view_bytes": 16383.0, "config_bytes": 1024.0}) {
		t.Fatalf("effective_caps = %v", c)
	}
	postCapsCheckin(t, f, "0.6.0", nil)
	c = knobEffectiveCaps(t, f)
	if c["source"] != capsSourceLegacy || !reflect.DeepEqual(c["pages"], []any{"bot", "pomodoro", "weather"}) ||
		!reflect.DeepEqual(c["features"], []any{}) || c["limits"] != nil {
		t.Fatalf("effective_caps after a downgrade = %v, want the legacy floor", c)
	}
}

func TestInvalidCapsAreDroppedWithoutAck(t *testing.T) {
	cases := map[string]any{
		"view not a pair":    map[string]any{"view": []int{1}, "pages": []string{"bot"}},
		"view min over max":  map[string]any{"view": []int{2, 1}, "pages": []string{"bot"}},
		"view zero":          map[string]any{"view": []int{0, 1}, "pages": []string{"bot"}},
		"no pages":           map[string]any{"view": []int{1, 1}, "pages": []string{}},
		"bad page id":        map[string]any{"view": []int{1, 1}, "pages": []string{"Bot"}},
		"duplicate page":     map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "bot"}},
		"bad feature":        map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "features": []string{"np-control"}},
		"negative limit":     map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "limits": map[string]any{"view_bytes": -1}},
		"bad rotation":       map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []int{0, 45}},
		"rotations sans 0":   map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []int{180}},
		"duplicate rotation": map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []int{0, 180, 180}},
		"rotations not ints": map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []string{"0"}},
		"null rotation":      map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []any{nil, 180}},
		"float rotation":     map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "rotations": []any{0, 180.5}},
		"not an object":      "caps",
	}
	for name, caps := range cases {
		t.Run(name, func(t *testing.T) {
			f := newViewFixture(t)
			postCapsCheckin(t, f, "0.10.0", knobCapsFull)
			reply := postCapsCheckin(t, f, "0.10.0", caps)
			if _, ok := reply["caps_ack"]; ok {
				t.Errorf("invalid caps acked: %v", reply)
			}
			if c := knobEffectiveCaps(t, f); c["source"] != capsSourceLegacy {
				t.Errorf("effective_caps = %v, want legacy after invalid caps", c)
			}
		})
	}
}

func TestCapsKnobIgnoresUnknownCapsFields(t *testing.T) {
	f := newViewFixture(t)
	reply := postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}, "future": map[string]any{"x": 1},
		"limits": map[string]any{"view_bytes": 4096, "flash_bytes": 9}})
	if reply["caps_ack"] != true {
		t.Fatalf("reply = %v, want caps_ack", reply)
	}
}

func TestCapsCheckinWritesStoreOnlyWhenCapsChange(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	kv, clk := countDeviceWrites(t, app)
	m := mintKnob(t, srv, http.StatusCreated)
	post := func(caps any) {
		t.Helper()
		resp, b := devReq(t, srv, "POST", "/v1/devices/self/checkin", m.Token, capsCheckinBody(t, "0.10.0", 1, caps))
		mustOK(t, "checkin", resp, b)
		clk.advance(time.Minute)
	}
	base := kv.puts.Load()
	post(knobCapsFull)
	if got := kv.puts.Load() - base; got != 1 {
		t.Fatalf("store writes for new caps = %d, want 1", got)
	}
	for range 3 {
		post(knobCapsFull)
	}
	if got := kv.puts.Load() - base; got != 1 {
		t.Fatalf("store writes for unchanged caps = %d, want still 1", got)
	}
	post(map[string]any{"view": []int{1, 1}, "pages": []string{"bot"}})
	if got := kv.puts.Load() - base; got != 2 {
		t.Fatalf("store writes after a caps change = %d, want 2", got)
	}
	post(nil)
	if got := kv.puts.Load() - base; got != 3 {
		t.Fatalf("store writes after caps went away = %d, want 3", got)
	}
	post(nil)
	if got := kv.puts.Load() - base; got != 3 {
		t.Fatalf("store writes for a second legacy checkin = %d, want 3", got)
	}

	app.devices.mu.Lock()
	app.devices.state.Devices[0].Caps = nil
	app.devices.mu.Unlock()
	post(knobCapsFull)
	if err := app.devices.load(); err != nil {
		t.Fatal(err)
	}
	if c := app.devices.list()[0].EffectiveCaps; c == nil || c.Source != capsSourceReported {
		t.Fatalf("caps not persisted: %+v", c)
	}
}

func TestClockRecordHasNoEffectiveCaps(t *testing.T) {
	d := deviceRecord{ID: "clock-abcdef", Kind: deviceKindClock}
	if v := d.view(); v.EffectiveCaps != nil {
		t.Fatalf("clock effective_caps = %+v, want none", v.EffectiveCaps)
	}
}

func TestDeviceCheckinCapsGolden(t *testing.T) {
	f := newViewFixture(t)
	body := capsCheckinBody(t, "0.10.0", 1, knobCapsFull)
	assertDeviceGolden(t, "checkin_req_caps", []byte(body))
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, body)
	assertDeviceGolden(t, "checkin_reply_caps", mustOK(t, "checkin", resp, b))
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
