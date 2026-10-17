package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
)

const testCapabilitiesJSON = `{"effects":["Fade","Matrix","Snake"],"paletteEffects":["Fade"],
	"transitions":["Slide","Dim"],"overlays":["rain"],"palettes":["Ocean"],
	"audio":{"buzzer":true,"track":false,"mp3":false,"radio":false},"scriptUpdates":true,
	"gpio":{"soc":"esp32","label":"ESP32","max":39}}`

func fakeClockDevice(t *testing.T, capsStatus int) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/capabilities":
			hits++
			w.WriteHeader(capsStatus)
			if capsStatus == http.StatusOK {
				_, _ = w.Write([]byte(testCapabilitiesJSON))
			}
		case "/api/v1/device":
			_, _ = w.Write([]byte(`{"version":"1.0.13","uid":"abc","boardType":"awtrixng"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func capsApp(t *testing.T, baseURL string) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = baseURL
	cfg.applyDefaults()
	return NewApp(cfg, &recordingPublisher{}, testLogger())
}

func TestRefreshCapabilitiesCachesFirmwareAndLists(t *testing.T) {
	srv, _ := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())

	caps, ok := app.capabilities()
	if !ok {
		t.Fatal("capabilities not cached")
	}
	if len(caps.Effects) != 3 || len(caps.Transitions) != 2 {
		t.Errorf("caps = %+v", caps)
	}
	if got := app.deviceFirmware(); got != "1.0.13" {
		t.Errorf("firmware = %q, want 1.0.13", got)
	}
}

func TestRefreshCapabilitiesLeavesCacheEmptyOnFailure(t *testing.T) {
	srv, _ := fakeClockDevice(t, http.StatusInternalServerError)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())
	if _, ok := app.capabilities(); ok {
		t.Fatal("a failed fetch must not populate the cache")
	}
}

func TestDeviceCapabilitiesEndpointServesCacheWithoutTouchingDevice(t *testing.T) {
	srv, hits := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())
	before := *hits

	w := httptest.NewRecorder()
	app.handleDeviceCapabilities(w, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if *hits != before {
		t.Errorf("a cached read must not hit the clock (%d → %d)", before, *hits)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, w.Body.String())
	}
	for _, k := range []string{"effects", "paletteEffects", "transitions", "overlays", "palettes", "audio", "scriptUpdates", "gpio"} {
		if _, ok := got[k]; !ok {
			t.Errorf("response missing %q: %s", k, w.Body.String())
		}
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestDeviceCapabilitiesEndpointFallsBackToLiveFetch(t *testing.T) {
	srv, hits := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)

	w := httptest.NewRecorder()
	app.handleDeviceCapabilities(w, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if *hits != 1 {
		t.Errorf("empty cache should proxy once, got %d hits", *hits)
	}
	if !strings.Contains(w.Body.String(), `"paletteEffects"`) {
		t.Errorf("body = %s", w.Body.String())
	}
	w2 := httptest.NewRecorder()
	app.handleDeviceCapabilities(w2, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if *hits != 1 {
		t.Errorf("second read should be served from cache, got %d hits", *hits)
	}
}

func TestDeviceCapabilitiesEndpointBadGatewayWhenClockUnreachable(t *testing.T) {
	app := capsApp(t, "http://127.0.0.1:1")
	w := httptest.NewRecorder()
	app.handleDeviceCapabilities(w, httptest.NewRequest(http.MethodGet, "/v1/device/capabilities", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestDoctorReportsCapabilityCounts(t *testing.T) {
	srv, _ := fakeClockDevice(t, http.StatusOK)
	app := capsApp(t, srv.URL)
	app.refreshCapabilities(context.Background())

	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	c, ok := res.Checks["capabilities"]
	if !ok {
		t.Fatal("doctor has no capabilities check")
	}
	if c.Status != StatusOK {
		t.Fatalf("status = %q detail = %q", c.Status, c.Detail)
	}
	for _, want := range []string{"effects=3", "transitions=2", "buzzer=true", "firmware=1.0.13"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q missing %q", c.Detail, want)
		}
	}
}

func TestDoctorWarnsWhenCapabilitiesNotFetched(t *testing.T) {
	app := capsApp(t, "http://127.0.0.1:1")
	res := runDoctorChecks(context.Background(), app, app.cfg.Load())
	if got := res.Checks["capabilities"].Status; got != StatusWarn {
		t.Fatalf("status = %q, want warn", got)
	}
}

type ngAudioClock struct {
	mu          sync.Mutex
	online      bool
	capsFail    bool
	version     string
	uptime      int64
	capsGate    chan struct{}
	notifies    []string
	plays       []string
	capsCalls   int
	capsWaiting bool
	refuseSound bool
}

func (c *ngAudioClock) set(fn func(c *ngAudioClock)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

func (c *ngAudioClock) snapshot() (notifies, plays []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.notifies...), append([]string(nil), c.plays...)
}

func (c *ngAudioClock) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		online, ng12, gate, refuseSound := c.online, c.version == "1.2.2", c.capsGate, c.refuseSound
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !online {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		refuse := func(field string) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"error":{"code":"validationFailed","message":"unknown key","field":"`+field+`"}}`)
		}
		switch r.URL.Path {
		case "/api/v1/device":
			c.mu.Lock()
			c.uptime += 30
			up := c.uptime
			v := c.version
			c.mu.Unlock()
			_, _ = io.WriteString(w, `{"version":"`+v+`","uid":"abc","boardType":"awtrixng","uptimeSeconds":`+strconv.FormatInt(up, 10)+`}`)
		case "/api/v1/capabilities":
			if gate != nil {
				c.mu.Lock()
				c.capsWaiting = true
				c.mu.Unlock()
				<-gate
			}
			c.mu.Lock()
			c.capsCalls++
			fail := c.capsFail
			c.mu.Unlock()
			switch {
			case fail:
				w.WriteHeader(http.StatusInternalServerError)
			case ng12:
				_, _ = io.WriteString(w, `{"audio":{"mp3":false,"rtttl":true,"song":false,"speech":false,"track":false,"radio":false,"url":false,"effect":false,"clip":false}}`)
			default:
				_, _ = io.WriteString(w, `{"audio":{"buzzer":true,"track":false,"mp3":false,"radio":false}}`)
			}
		case "/api/v1/notifications":
			c.mu.Lock()
			c.notifies = append(c.notifies, string(body))
			c.mu.Unlock()
			if refuseSound && strings.Contains(string(body), `"sound"`) {
				refuse("sound")
				return
			}
			if ng12 && (strings.Contains(string(body), `"soundRtttl"`) || strings.Contains(string(body), `"soundLoop"`)) {
				refuse("soundRtttl")
				return
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/api/v1/audio/play":
			c.mu.Lock()
			c.plays = append(c.plays, string(body))
			c.mu.Unlock()
			if ng12 && strings.Contains(string(body), `"melody"`) {
				refuse("melody")
				return
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ngAudioApp(t *testing.T, clock *ngAudioClock) *App {
	t.Helper()
	srv := clock.server(t)
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv.URL
	cfg.applyDefaults()
	a := NewApp(cfg, nil, testLogger())
	t.Cleanup(a.clockJobs.Wait)
	return a
}

func chime(t *testing.T, a *App) {
	t.Helper()
	n := notice{app: "test", kind: noticeMessage, sound: noticeSound{rtttl: "x:d=4,o=5,b=120:c"}, payload: map[string]any{"text": "hi"}}
	if err := a.coord.showNoticeQuiet(t.Context(), n, false); err != nil {
		t.Fatalf("notice: %v", err)
	}
}

func lastNotify(t *testing.T, clock *ngAudioClock) string {
	t.Helper()
	notifies, _ := clock.snapshot()
	if len(notifies) == 0 {
		t.Fatal("no notification reached the clock")
	}
	return notifies[len(notifies)-1]
}

func TestColdStartFillsCapsOnceTheClockComesOnline(t *testing.T) {
	clock := &ngAudioClock{version: "1.2.2"}
	a := ngAudioApp(t, clock)
	a.refreshCapabilities(t.Context())
	if _, ok := a.capabilities(); ok {
		t.Fatal("offline clock produced capabilities")
	}
	clock.set(func(c *ngAudioClock) { c.online = true })
	a.watchTick(t.Context(), deviceProbe{}, 0)
	if !a.soundObjectAPI() {
		t.Fatal("watch tick on a reachable clock with no caps did not fetch them")
	}
	chime(t, a)
	if notifies, _ := clock.snapshot(); len(notifies) != 1 || !strings.Contains(notifies[0], `"sound":{"rtttl"`) {
		t.Fatalf("notifies = %v", notifies)
	}
}

func TestCapsRecoverAfterAFailedFetch(t *testing.T) {
	clock := &ngAudioClock{version: "1.2.2", online: true, capsFail: true}
	a := ngAudioApp(t, clock)
	a.refreshCapabilities(t.Context())
	if _, ok := a.capabilities(); ok {
		t.Fatal("failed fetch cached caps")
	}
	clock.set(func(c *ngAudioClock) { c.capsFail = false })
	a.watchTick(t.Context(), deviceProbe{}, 0)
	if !a.soundObjectAPI() {
		t.Fatal("caps did not recover on the next watch tick")
	}
}

func TestSameURLFirmwareUpgradeRefreshesCapsOnReboot(t *testing.T) {
	clock := &ngAudioClock{version: "1.1.2", online: true, uptime: 1000}
	a := ngAudioApp(t, clock)
	a.refreshCapabilities(t.Context())
	last := a.watchTick(t.Context(), deviceProbe{}, 0)
	if a.soundObjectAPI() {
		t.Fatal("1.1 clock read as 1.2")
	}
	clock.set(func(c *ngAudioClock) { c.version, c.uptime = "1.2.2", 5 })
	a.watchTick(t.Context(), last, 0)
	a.clockJobs.Wait()
	if !a.soundObjectAPI() {
		t.Fatal("reboot after a same-URL upgrade kept the 1.1 caps")
	}
}

func TestClockConfigPutInvalidatesCapsAndTheWatchRefills(t *testing.T) {
	clock := &ngAudioClock{version: "1.2.2", online: true}
	a := ngAudioApp(t, clock)
	a.refreshCapabilities(t.Context())
	base := a.cfg.Load().effectiveClockURL()
	w := putDeviceConfig(a, `{"base_url":"`+base+`"}`)
	a.clockJobs.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("config put: %d %s", w.Code, w.Body)
	}
	if _, ok := a.capabilities(); ok {
		t.Fatal("config PUT kept the caps")
	}
	a.watchTick(t.Context(), deviceProbe{}, 0)
	if !a.soundObjectAPI() {
		t.Fatal("watch tick did not refill caps after the config PUT")
	}
}

func TestLateCapsFetchFromTheOldClockIsDropped(t *testing.T) {
	gate := make(chan struct{})
	oldClock := &ngAudioClock{version: "1.1.2", online: true, capsGate: gate}
	a := ngAudioApp(t, oldClock)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.refreshCapabilities(context.Background())
	}()
	waitForCapsRequest(t, oldClock, done)
	newClock := &ngAudioClock{version: "1.2.2", online: true}
	newSrv := newClock.server(t)
	if err := putClockOverride(a, newSrv.URL); err != nil {
		t.Fatal(err)
	}
	a.refreshCapabilities(t.Context())
	close(gate)
	<-done
	caps, ok := a.capabilities()
	if !ok || !caps.Audio.SoundObjectAPI() {
		t.Fatalf("caps = %+v ok=%v, want the new clock's 1.2 caps", caps.Audio, ok)
	}
}

func TestNoticeRefusedIn11FormatRetriesIn12Format(t *testing.T) {
	for _, cached := range []string{"cold", "stale 1.1"} {
		t.Run(cached, func(t *testing.T) {
			clock := &ngAudioClock{version: "1.2.2", online: true}
			a := ngAudioApp(t, clock)
			if cached == "stale 1.1" {
				withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
			}
			chime(t, a)
			notifies, _ := clock.snapshot()
			if len(notifies) != 2 || !strings.Contains(notifies[0], `"soundRtttl"`) || !strings.Contains(notifies[1], `"sound":{"rtttl"`) {
				t.Fatalf("notifies = %v, want a refused 1.1 payload then one 1.2 retry", notifies)
			}
		})
	}
}

func TestNoticeWithASoundTheClockRefusesStillShows(t *testing.T) {
	clock := &ngAudioClock{version: "1.2.2", online: true, refuseSound: true}
	a := ngAudioApp(t, clock)
	withAudioCaps(a, ng12Audio(true))
	n := notice{app: "pomodoro", kind: noticePomodoro, sound: soundSetting("Star Wars"), payload: map[string]any{"text": "BREAK"}}
	if err := a.coord.showNoticeQuiet(t.Context(), n, false); err != nil {
		t.Fatalf("notice: %v", err)
	}
	notifies, _ := clock.snapshot()
	if len(notifies) != 2 || !strings.Contains(notifies[0], `"sound":"Star Wars"`) ||
		strings.Contains(notifies[1], `"sound"`) || !strings.Contains(notifies[1], `"BREAK"`) {
		t.Fatalf("notifies = %v, want the refused sound then the popup without it", notifies)
	}
}

func TestNoticeRefusedForAnotherReasonStopsAfterTheSoundlessResend(t *testing.T) {
	clock := &ngAudioClock{version: "1.2.2", online: true}
	a := ngAudioApp(t, clock)
	withAudioCaps(a, ng12Audio(true))
	n := notice{app: "test", kind: noticeMessage, sound: noticeSound{rtttl: "x:d=4:c"},
		payload: map[string]any{"text": "hi", "soundLoop": true}}
	if err := a.coord.showNoticeQuiet(t.Context(), n, false); !isUnprocessable(err) {
		t.Fatalf("err = %v, want the clock's 422", err)
	}
	notifies, _ := clock.snapshot()
	if len(notifies) != 2 || strings.Contains(notifies[1], `"sound":`) {
		t.Fatalf("notifies = %v, want one attempt with the sound and one without", notifies)
	}
}

func TestRediscoverySwapDropsAnInFlightFetchFromTheOldClock(t *testing.T) {
	oldGate, newGate := make(chan struct{}), make(chan struct{})
	oldClock := &ngAudioClock{version: "1.1.2", online: true, capsGate: oldGate}
	a := ngAudioApp(t, oldClock)
	newClock := &ngAudioClock{version: "1.2.2", online: true, capsGate: newGate}
	newSrv := newClock.server(t)
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: newSrv.URL, UID: "abc"}}, nil
	}
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		a.refreshCapabilities(context.Background())
	}()
	waitForCapsRequest(t, oldClock, oldDone)
	oldClock.set(func(c *ngAudioClock) { c.online = false })
	swapDone := make(chan bool)
	go func() { swapDone <- a.rediscoverClock(context.Background()) }()
	waitForCapsRequest(t, newClock, nil)
	close(oldGate)
	<-oldDone
	close(newGate)
	if !<-swapDone {
		t.Fatal("rediscovery did not swap")
	}
	caps, ok := a.capabilities()
	if !ok || !caps.Audio.SoundObjectAPI() {
		t.Fatalf("caps = %+v ok=%v, want the new clock's 1.2 caps", caps.Audio, ok)
	}
}

func TestMelodyPreviewRefusedIn11FormatRetriesAsFile(t *testing.T) {
	clock := &ngAudioClock{version: "1.2.2", online: true}
	a := ngAudioApp(t, clock)
	withAudioCaps(a, awtrix.AudioCaps{Buzzer: true})
	w := httptest.NewRecorder()
	a.handleDeviceAudioTest(w, httptest.NewRequest("POST", "/v1/device/audio/test", strings.NewReader(`{"melody":"bell"}`)))
	_, plays := clock.snapshot()
	if w.Code != http.StatusOK || len(plays) != 2 || plays[0] != `{"melody":"bell"}` || plays[1] != `{"file":"bell"}` {
		t.Fatalf("code=%d plays=%v", w.Code, plays)
	}
}

func waitForCapsRequest(t *testing.T, c *ngAudioClock, done <-chan struct{}) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		c.mu.Lock()
		waiting := c.capsWaiting
		c.mu.Unlock()
		if waiting {
			return
		}
		select {
		case <-done:
			t.Fatal("refresh returned before the gated fetch")
		case <-deadline:
			t.Fatal("caps request never arrived")
		case <-time.After(time.Millisecond):
		}
	}
}
