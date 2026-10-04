package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
	"github.com/tarakanof/ember/internal/render"
)

var parityPatterns = map[string][]bool{
	"none":    {false},
	"lossy44": {false, true, false, true, false, false, true, false, true},
	"lossy60": {true, false, true, true, false, true, false, true, true, false},
}

const paritySlowDelay = 3 * time.Second

type parityClock struct {
	t          *testing.T
	mu         sync.Mutex
	pat        []bool
	n          int
	log        *strings.Builder
	slowNext   bool
	pushFaults []string

	conns    map[net.Conn]int
	uptime   int64
	settings map[string]any
	system   map[string]any
	apps     []string
}

func newParityClock(t *testing.T, pat []bool, log *strings.Builder) *parityClock {
	return &parityClock{
		t:        t,
		pat:      pat,
		log:      log,
		conns:    map[net.Conn]int{},
		uptime:   1000,
		settings: map[string]any{"autoTransition": true, "blockNavigation": false, "brightness": 90.0, "textColor": "#FFFFFF"},
		system:   map[string]any{"tempOffset": -9.0, "humOffset": 0.0, "buttonCallback": "", "wifiPassword": "secret"},
		apps:     []string{"Time", "Date", "ember-weather"},
	}
}

func (f *parityClock) connID(c net.Conn) int {
	id, ok := f.conns[c]
	if !ok {
		id = len(f.conns) + 1
		f.conns[c] = id
	}
	return id
}

func (f *parityClock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	n := f.n
	f.n++
	drop := f.pat[n%len(f.pat)]
	slow := f.slowNext
	if slow {
		f.slowNext, drop = false, false
	}
	delay := paritySlowDelay
	fault := ""
	if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/apps/pushed/") && len(f.pushFaults) > 0 {
		fault, f.pushFaults = f.pushFaults[0], f.pushFaults[1:]
		drop = false
		switch fault {
		case "slow1800":
			slow, delay = true, 1800*time.Millisecond
		case "slow3500":
			slow, delay = true, 3500*time.Millisecond
		}
	}
	f.connID(r.Context().Value(parityConnKey{}).(net.Conn))
	h := sha256.Sum256(body)
	outcome := "ok"
	switch {
	case drop:
		outcome = "DROP"
	case fault != "":
		outcome = fault
	case slow:
		outcome = "slow"
	}
	fmt.Fprintf(f.log, "  dev#%03d %s %s body=%x %s\n", n, r.Method, r.URL.RequestURI(), h[:4], outcome)
	f.mu.Unlock()

	if drop {
		hj, ok := w.(http.Hijacker)
		if !ok {
			f.t.Fatal("no hijacker")
		}
		c, _, err := hj.Hijack()
		if err == nil {
			_, _ = io.WriteString(c, "HTTP/1.1 ")
			c.Close()
		}
		return
	}
	if slow {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if fault == "503" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, ngError("serviceBusy", "busy", ""))
		return
	}
	f.mu.Lock()
	status, out := f.answer(r.Method, r.URL.Path, body)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, out)
}

func ngError(code, msg, field string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": msg, "field": field}})
	return string(b)
}

func (f *parityClock) answer(method, path string, body []byte) (int, string) {
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	ok := `{"ok":true}`
	switch {
	case path == "/api/v1/device" && method == http.MethodGet:
		f.uptime += 30
		return 200, js(map[string]any{"uid": "abc", "boardType": "awtrixng", "version": "1.1.2", "uptimeSeconds": f.uptime, "currentApp": "Time"})
	case path == "/api/v1/capabilities":
		return 200, `{"effects":["Fade"],"paletteEffects":[],"transitions":["slide"],"overlays":["rain"],"palettes":["Rainbow"],"audio":{"buzzer":true}}`
	case path == "/api/v1/settings" && method == http.MethodGet:
		return 200, js(f.settings)
	case path == "/api/v1/settings" && method == http.MethodPatch:
		if _, bad := in["uppercase"]; bad {
			return 422, ngError("validationFailed", "not on this build", "uppercase")
		}
		for k, v := range in {
			f.settings[k] = v
		}
		return 200, ok
	case path == "/api/v1/system" && method == http.MethodGet:
		return 200, js(f.system)
	case path == "/api/v1/system" && method == http.MethodPut:
		if in["tempOffset"] == 7.0 {
			return 422, ngError("validationFailed", "out of range", "tempOffset")
		}
		f.system = in
		return 200, ok
	case path == "/api/v1/display" && method == http.MethodGet:
		return 200, `{"power":true,"overlay":null}`
	case path == "/api/v1/display/screen":
		return 200, `{"width":32,"height":8,"pixels":[0]}`
	case path == "/api/v1/apps" && method == http.MethodGet:
		list := make([]map[string]any, 0, len(f.apps))
		for _, a := range f.apps {
			origin := "builtin"
			if strings.HasPrefix(a, "ember") {
				origin = "pushed"
			}
			list = append(list, map[string]any{"name": a, "enabled": true, "inLoop": true, "origin": origin})
		}
		return 200, js(list)
	case strings.HasPrefix(path, "/api/v1/apps/pushed/") && method == http.MethodPut:
		name := strings.TrimPrefix(path, "/api/v1/apps/pushed/")
		if !containsStr(f.apps, name) {
			f.apps = append(f.apps, name)
		}
		return 200, ok
	case path == "/api/v1/apps/active" && method == http.MethodPut:
		if !containsStr(f.apps, fmt.Sprint(in["name"])) {
			return 404, ngError("notFound", "no such app", "name")
		}
		return 200, ok
	case path == "/api/v1/apps/order":
		return 200, ok
	case path == "/api/v1/apps/next" || path == "/api/v1/apps/previous":
		return 200, ok
	case strings.HasPrefix(path, "/api/v1/apps/script/"):
		return 404, ngError("notFound", "no script", "")
	case strings.HasPrefix(path, "/api/v1/apps/") && method == http.MethodDelete:
		name := strings.TrimPrefix(path, "/api/v1/apps/")
		for i, a := range f.apps {
			if a == name {
				f.apps = append(f.apps[:i], f.apps[i+1:]...)
				return 200, ok
			}
		}
		return 404, ngError("notFound", "no such app", "")
	case strings.HasPrefix(path, "/api/v1/notifications"):
		return 200, ok
	case strings.HasPrefix(path, "/api/v1/indicators/"):
		return 200, ok
	case path == "/api/v1/audio/play" || path == "/api/v1/audio/stop":
		return 200, ok
	case path == "/api/v1/audio/melodies":
		return 200, `{"melodies":[],"usedBytes":0,"totalBytes":4096}`
	case path == "/api/v1/device/reboot":
		return 503, ngError("serviceBusy", "busy", "")
	}
	return 404, ngError("notFound", "no route", "")
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type parityConnKey struct{}

var parityPort = regexp.MustCompile(`127\.0\.0\.1:\d+`)

func TestClockParity(t *testing.T) {
	if testing.Short() {
		t.Skip("clock parity harness sleeps past the probe timeouts")
	}
	for _, name := range []string{"none", "lossy44", "lossy60"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := runClockParity(t, parityPatterns[name])
			path := filepath.Join("testdata", "clock_parity", name+".txt")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(want) != got {
				t.Errorf("device traffic differs from %s:\n%s", path, lineDiff(string(want), got))
			}
		})
	}
}

func lineDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "line %d\n  want: %s\n  got:  %s\n", i+1, wl, gl)
			if shown++; shown >= 8 {
				break
			}
		}
	}
	return b.String()
}

func runClockParity(t *testing.T, pat []bool) string {
	var log strings.Builder
	dev := newParityClock(t, pat, &log)
	srv := httptest.NewUnstartedServer(dev)
	srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, parityConnKey{}, c)
	}
	srv.Start()
	t.Cleanup(srv.Close)
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv.URL
	cfg.Display.Indicators = true
	cfg.Display.AttentionChime = true
	cfg.HTTP.Addr = ":3627"
	cfg.applyDefaults()
	app := NewApp(cfg, nil, testLogger())
	app.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: srv.URL, UID: "abc"}}, nil
	}
	night := time.Date(2026, 9, 26, 23, 0, 0, 0, time.Local)
	qp := app.publisher.(*quietPublisher)
	qp.now = func() time.Time { return night }

	step := func(format string, args ...any) {
		dev.mu.Lock()
		fmt.Fprintf(&log, format+"\n", args...)
		dev.mu.Unlock()
	}
	norm := func(s string) string {
		s = strings.ReplaceAll(s, srv.URL, "CLOCK")
		return parityPort.ReplaceAllString(s, "HOST")
	}

	c := app.coord
	clk := newFakeClock()
	c.clk = clk
	c.ctx = context.Background()
	state := "running"
	sessAt := time.Unix(1_700_000_000, 0)
	c.snapshot = func() Snapshot {
		return Snapshot{Now: sessAt, Sessions: []render.Session{
			{Source: "mbp", Tool: "claude", Session: "a", State: state, UpdatedAt: sessAt},
		}}
	}
	pomo := false
	c.pomoView = func() (render.PomodoroView, bool) {
		if !pomo {
			return render.PomodoroView{}, false
		}
		return render.PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500, FocusColor: render.RGB{R: 0xff}}, true
	}
	for tick := 0; tick < 40; tick++ {
		step("tick %02d state=%s pomo=%v hold=%d", tick, state, pomo, c.hold)
		switch tick {
		case 5, 15, 24:
			dev.mu.Lock()
			dev.pushFaults = append(dev.pushFaults, map[int]string{5: "slow1800", 15: "slow3500", 24: "503"}[tick])
			dev.mu.Unlock()
		case 3:
			state = "waiting"
			c.onUpsert("mbp/claude/a", "running", "waiting")
		case 8:
			state = "running"
			c.onUpsert("mbp/claude/a", "waiting", "running")
		case 12:
			pomo = true
		case 20:
			c.onRepublish()
		case 27:
			pomo = false
		case 33:
			state = "error"
			c.onUpsert("mbp/claude/a", "running", "error")
		}
		c.onTick()
		clk.Advance(37 * time.Second)
	}

	for _, quiet := range []bool{false, true} {
		cur := *app.cfg.Load()
		cur.QuietHours = QuietHoursConfig{Enabled: quiet, Start: "22:00", End: "08:00"}
		app.cfg.Store(&cur)
		for i := 0; i < 3; i++ {
			err := app.publisher.Notify(context.Background(), map[string]any{"text": "hi", "sound": "chime", "soundRtttl": "x:d=4:c"})
			step("quiet=%v notify #%d err=%v", quiet, i, norm(fmt.Sprint(err)))
			err = app.publisher.PlayRTTTL(context.Background(), "x:d=4:c")
			step("quiet=%v rtttl #%d err=%v", quiet, i, norm(fmt.Sprint(err)))
		}
	}
	quietOff := *app.cfg.Load()
	quietOff.QuietHours.Enabled = false
	app.cfg.Store(&quietOff)

	type call struct {
		name string
		h    http.HandlerFunc
		body string
	}
	calls := []call{
		{"settings.get", app.handleDeviceSettingsGet, ""},
		{"settings.put", app.handleDeviceSettingsPut, `{"brightness":50}`},
		{"settings.put.takeover_key", app.handleDeviceSettingsPut, `{"autoTransition":true}`},
		{"settings.put.refused", app.handleDeviceSettingsPut, `{"uppercase":true}`},
		{"stats", app.handleDeviceStats, ""},
		{"screen", app.handleDeviceScreen, ""},
		{"display.get", app.handleDeviceDisplayGet, ""},
		{"display.put", app.handleDeviceDisplayPut, `{"overlay":"rain"}`},
		{"apps.get", app.handleDeviceAppsGet, ""},
		{"apps.put", app.handleDeviceAppsPut, `{"order":["Time"]}`},
		{"next", app.handleDeviceNextApp, ""},
		{"prev", app.handleDevicePrevApp, ""},
		{"dismiss", app.handleDeviceDismiss, ""},
		{"reboot", app.handleDeviceReboot, ""},
		{"power", app.handleDevicePowerPut, `{"power":true}`},
		{"audio.test", app.handleDeviceAudioTest, ``},
		{"audio.stop", app.handleDeviceAudioStop, ""},
		{"audio.melodies", app.handleDeviceAudioMelodies, ""},
		{"capabilities", app.handleDeviceCapabilities, ""},
		{"sensors.get", app.handleDeviceSensorsGet, ""},
		{"sensors.put", app.handleDeviceSensorsPut, `{"temp_offset":-4}`},
		{"sensors.put.refused", app.handleDeviceSensorsPut, `{"temp_offset":7}`},
		{"buttons.get", app.handleDeviceButtons, ""},
		{"buttons.put", app.handleDeviceButtonsPut, `{"enabled":true}`},
	}
	for round := 0; round < 3; round++ {
		for _, cl := range calls {
			var body io.Reader
			if cl.body != "" {
				body = strings.NewReader(cl.body)
			}
			w := httptest.NewRecorder()
			cl.h(w, httptest.NewRequest(http.MethodPut, "/v1/device/x", body))
			out := strings.TrimSpace(norm(w.Body.String()))
			if cl.name == "buttons.get" {
				out = regexp.MustCompile(`"last_press_unix":\d+`).ReplaceAllString(out, `"last_press_unix":N`)
			}
			step("menu %s r%d → %d %s", cl.name, round, w.Code, out)
		}
		app.caps.Store(nil)
	}

	slow := func() {
		dev.mu.Lock()
		dev.slowNext = true
		dev.mu.Unlock()
	}

	slow()
	step("slow publish err=%v", norm(fmt.Sprint(app.publisher.Notify(context.Background(), map[string]any{"text": "slow"}))))
	slow()
	sw := httptest.NewRecorder()
	app.handleDeviceStats(sw, httptest.NewRequest(http.MethodGet, "/v1/device/stats", nil))
	step("slow menu stats → %d", sw.Code)
	slow()
	step("slow probe reachable=%v", app.probeDevice(context.Background(), 0).reachable)
	slow()
	app.refreshCapabilities(context.Background())
	_, capsOK := app.capabilities()
	step("slow capabilities cached=%v", capsOK)
	slow()
	sr := checkAWTRIXReachable(context.Background(), app.cfg.Load())
	step("slow doctor.awtrix %s", sr.Status)
	slow()
	step("slow doctor.clock reachable=%v", *checkClock(context.Background(), app).Reachable)
	slow()
	step("slow rediscover swapped=%v", app.rediscoverClock(context.Background()))

	for i := 0; i < 4; i++ {
		p := app.probeDevice(context.Background(), 0)
		step("probe #%d reachable=%v uptime=%d", i, p.reachable, p.uptimeSec)
	}
	for i := 0; i < 3; i++ {
		swapped := app.rediscoverClock(context.Background())
		res, _ := app.lastRediscoverResult.Load().(string)
		step("rediscover #%d swapped=%v result=%s", i, swapped, res)
	}
	for i := 0; i < 3; i++ {
		app.refreshCapabilities(context.Background())
		_, ok := app.capabilities()
		step("capabilities #%d cached=%v", i, ok)
	}
	for i := 0; i < 3; i++ {
		r := checkAWTRIXReachable(context.Background(), app.cfg.Load())
		d := regexp.MustCompile(`\(\d+(\.\d+)?m?s\)`).ReplaceAllString(norm(r.Detail), "(T)")
		step("doctor.awtrix #%d %s %s", i, r.Status, d)
		cr := checkClock(context.Background(), app)
		step("doctor.clock #%d %s reachable=%v", i, cr.Status, *cr.Reachable)
	}

	cur := *app.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = ""
	app.cfg.Store(&cur)
	err := app.publisher.Notify(context.Background(), map[string]any{"text": "x"})
	step("unconfigured notify err=%v", err)
	w := httptest.NewRecorder()
	app.handleDeviceStats(w, httptest.NewRequest(http.MethodGet, "/v1/device/stats", nil))
	step("unconfigured stats → %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	w = httptest.NewRecorder()
	app.handleDeviceSensorsPut(w, httptest.NewRequest(http.MethodPut, "/v1/device/sensors", strings.NewReader(`{"temp_offset":1}`)))
	step("unconfigured sensors.put → %d %s", w.Code, strings.TrimSpace(w.Body.String()))

	dev.mu.Lock()
	defer dev.mu.Unlock()
	fmt.Fprintf(&log, "requests=%d\n", dev.n)
	if len(pat) == 1 && !pat[0] && len(dev.conns) > 10 {
		t.Errorf("connections = %d for %d requests on a lossless link, want <= 10 (keep-alive reuse broken)", len(dev.conns), dev.n)
	}
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(log.String()))
	for sc.Scan() {
		out.WriteString(norm(sc.Text()) + "\n")
	}
	return out.String()
}
