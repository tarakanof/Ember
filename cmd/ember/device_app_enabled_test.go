package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func newNGClockApp(t *testing.T, version string) (*App, *httptest.Server, *clockStub, func(name, body string) (*http.Response, []byte)) {
	t.Helper()
	a, srv, stub := newClockApp(t)
	stub.version = version
	a.refreshCapabilities(context.Background())
	put := func(name, body string) (*http.Response, []byte) {
		t.Helper()
		return devReq(t, srv, "PUT", "/v1/device/apps/"+name+"/enabled", testToken, body)
	}
	return a, srv, stub, put
}

func decodeOrderPut(t *testing.T, raw string) deviceAppsPutBody {
	t.Helper()
	var b deviceAppsPutBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("order body %q: %v", raw, err)
	}
	return b
}

func mustPut(t *testing.T, put func(name, body string) (*http.Response, []byte), what, name, body string) {
	t.Helper()
	resp, b := put(name, body)
	mustOK(t, what, resp, b)
}

func cacheRotation(t *testing.T, srv *httptest.Server) {
	t.Helper()
	resp, b := devReq(t, srv, "GET", "/v1/device/apps", testToken, "")
	mustOK(t, "apps read", resp, b)
}

func TestAppEnabledUsesPerAppRouteOnNG12(t *testing.T) {
	_, _, stub, put := newNGClockApp(t, "1.2.2")
	resp, b := put("time", `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	if got := stub.appEnabledPuts(); !slices.Equal(got, []string{"time=false"}) {
		t.Fatalf("per-app writes = %v, want [time=false]", got)
	}
	if n := len(stub.puts()); n != 0 {
		t.Fatalf("an NG 1.2 toggle also wrote the full order: %v", stub.puts())
	}
	if got := stub.enabledByName(); got["time"] != false || got["date"] != true || got["hum"] != false {
		t.Fatalf("clock apps = %v, want time off, date on, hum still off", got)
	}
}

func TestAppEnabledLeavesOtherAppsChangedMeanwhileOnNG12(t *testing.T) {
	_, _, stub, put := newNGClockApp(t, "1.2.2")
	stub.mu.Lock()
	stub.apps[2]["enabled"] = true
	stub.mu.Unlock()
	resp, b := put("date", `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	if got := stub.enabledByName(); got["hum"] != true || got["date"] != false {
		t.Fatalf("clock apps = %v, want hum left on as set elsewhere, date off", got)
	}
}

func TestAppEnabledTriesThePerAppRouteBeforeAnyProbe(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.version = "1.2.2"
	if v := a.deviceFirmware(); v != "" {
		t.Fatalf("firmware cached before any probe: %q", v)
	}
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps/time/enabled", testToken, `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	if got := stub.appEnabledPuts(); !slices.Equal(got, []string{"time=false"}) {
		t.Fatalf("per-app writes = %v, want the per-app route without a cached firmware", got)
	}
	if len(stub.puts()) != 0 {
		t.Fatalf("order writes = %v, want none", stub.puts())
	}
}

func TestAppEnabledFallsBackToFreshOrderWriteBeforeNG12(t *testing.T) {
	for _, version := range []string{"1.0.13", "1.1.2"} {
		t.Run(version, func(t *testing.T) {
			_, _, stub, put := newNGClockApp(t, version)
			stub.mu.Lock()
			stub.apps[1]["enabled"] = false
			stub.mu.Unlock()
			resp, b := put("hum", `{"enabled":true}`)
			mustOK(t, "app enabled", resp, b)
			if got := stub.appEnabledPuts(); len(got) != 1 {
				t.Fatalf("per-app writes = %v, want the one try", got)
			}
			puts := stub.puts()
			if len(puts) != 1 {
				t.Fatalf("order writes = %v, want one", puts)
			}
			body := decodeOrderPut(t, puts[0])
			if !slices.Equal(body.Order, []string{"time", "date", "hum", "ember"}) || !slices.Equal(body.Disabled, []string{"date"}) {
				t.Fatalf("order write = %+v, want every app in place and only date (switched off elsewhere) off", body)
			}
			if got := stub.enabledByName(); got["hum"] != true || got["date"] != false || got["time"] != true {
				t.Fatalf("clock apps = %v", got)
			}
		})
	}
}

func TestAppEnabledFallsBackWhenClockLacksTheRoute(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			_, _, stub, put := newNGClockApp(t, "1.2.2")
			stub.onStatus = status
			resp, b := put("date", `{"enabled":false}`)
			mustOK(t, "app enabled", resp, b)
			if got := stub.appEnabledPuts(); len(got) != 1 {
				t.Fatalf("per-app writes = %v, want the one try", got)
			}
			puts := stub.puts()
			if len(puts) != 1 {
				t.Fatalf("order writes = %v, want the fallback", puts)
			}
			if body := decodeOrderPut(t, puts[0]); !slices.Equal(body.Disabled, []string{"date", "hum"}) {
				t.Fatalf("fallback disabled = %v, want [date hum]", body.Disabled)
			}
		})
	}
}

func TestAppEnabledRemembersAClockWithoutTheRoute(t *testing.T) {
	_, _, stub, put := newNGClockApp(t, "1.1.2")
	mustPut(t, put, "first", "date", `{"enabled":false}`)
	mustPut(t, put, "second", "date", `{"enabled":true}`)
	if got := stub.appEnabledPuts(); len(got) != 1 {
		t.Fatalf("per-app writes = %v, want one try, then the remembered fallback", got)
	}
	if len(stub.puts()) != 2 {
		t.Fatalf("order writes = %v, want two", stub.puts())
	}
}

func TestAppEnabledRetriesThePerAppRouteAfterAFirmwareChange(t *testing.T) {
	a, _, stub, put := newNGClockApp(t, "1.1.2")
	mustPut(t, put, "on 1.1.2", "date", `{"enabled":false}`)
	stub.mu.Lock()
	stub.version = "1.2.2"
	stub.mu.Unlock()
	mustPut(t, put, "before the probe", "date", `{"enabled":true}`)
	if got := stub.appEnabledPuts(); len(got) != 1 {
		t.Fatalf("per-app writes before the probe saw 1.2.2 = %v, want 1", got)
	}
	if dev := a.probeClockHealthWithin(context.Background(), time.Now(), 0); dev == nil || !dev.Reachable {
		t.Fatalf("probe failed: %+v", dev)
	}
	mustPut(t, put, "after the probe", "date", `{"enabled":false}`)
	if got := stub.appEnabledPuts(); len(got) != 2 || got[1] != "date=false" {
		t.Fatalf("per-app writes = %v, want a fresh try once the probe saw new firmware", got)
	}
	if len(stub.puts()) != 2 {
		t.Fatalf("order writes = %v, want only the two from 1.1.2", stub.puts())
	}
	if got := stub.enabledByName(); got["date"] != false {
		t.Fatalf("clock apps = %v", got)
	}
}

func TestAppEnabledRetriesThePerAppRouteAfterAURLSwap(t *testing.T) {
	a, _, old, put := newNGClockApp(t, "1.1.2")
	mustPut(t, put, "old clock", "date", `{"enabled":false}`)
	next := newClockStub(t)
	next.version = "1.2.2"
	pointAtClock(a, next.URL)
	mustPut(t, put, "new clock", "date", `{"enabled":false}`)
	if got := next.appEnabledPuts(); !slices.Equal(got, []string{"date=false"}) || len(next.puts()) != 0 {
		t.Fatalf("new clock per-app %v order %v, want the per-app route tried again", got, next.puts())
	}
	if len(old.appEnabledPuts()) != 1 {
		t.Fatalf("old clock per-app writes = %v", old.appEnabledPuts())
	}
}

func TestAppEnabledRelaysOtherClockRejections(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			_, _, stub, put := newNGClockApp(t, "1.2.2")
			stub.onStatus = status
			resp, b := put("date", `{"enabled":false}`)
			if resp.StatusCode != status || !strings.Contains(string(b), "clock returned") {
				t.Fatalf("PUT = %d %s, want %d relaying the clock", resp.StatusCode, b, status)
			}
			if len(stub.puts()) != 0 {
				t.Fatalf("a refused toggle fell back to an order write: %v", stub.puts())
			}
		})
	}
}

func TestAppEnabledTreatsNotSavedAsApplied(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.version = "1.2.2"
	d := registeredClock(t, a, srv)
	_, v0 := getClockConfig(t, srv, d.ID)
	stub.onStatus = http.StatusInsufficientStorage
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps/date/enabled", testToken, `{"enabled":false}`)
	if resp.StatusCode != http.StatusInsufficientStorage || !strings.Contains(string(b), "applied, not saved yet") {
		t.Fatalf("PUT = %d %s, want 507 relaying NG's not-saved", resp.StatusCode, b)
	}
	after, v1 := getClockConfig(t, srv, d.ID)
	if after.Rotation == nil || !slices.Contains(after.Rotation.Disabled, "date") || v1 <= v0 {
		t.Fatalf("rotation %+v version %d -> %d, want the applied change read back", after.Rotation, v0, v1)
	}
}

func TestAppEnabledFallbackReadFailures(t *testing.T) {
	cases := []struct {
		name      string
		set       func(a *App, s *clockStub)
		forgotten bool
	}{
		{"clock 500", func(_ *App, s *clockStub) { s.appsFailN = 1 }, false},
		{"not JSON", func(_ *App, s *clockStub) { s.appsBody = "<html>" }, true},
		{"timeout", func(a *App, s *clockStub) {
			s.appsSlow = []time.Duration{5 * time.Second}
			a.rotationOp.budget.Store(int64(300 * time.Millisecond))
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, srv, stub, put := newNGClockApp(t, "1.1.2")
			cacheRotation(t, srv)
			stub.mu.Lock()
			c.set(a, stub)
			stub.mu.Unlock()
			resp, b := put("date", `{"enabled":false}`)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("PUT = %d %s, want 502", resp.StatusCode, b)
			}
			if len(stub.puts()) != 0 {
				t.Fatalf("order written without a list: %v", stub.puts())
			}
			if got := a.clockRotation.Load() == nil; got != c.forgotten {
				t.Fatalf("rotation forgotten = %v, want %v", got, c.forgotten)
			}
		})
	}
}

func TestAppEnabledFallbackWriteRefused(t *testing.T) {
	cases := []struct{ clock, want int }{
		{http.StatusBadRequest, http.StatusBadRequest},
		{http.StatusInternalServerError, http.StatusBadGateway},
		{http.StatusNotFound, http.StatusBadGateway},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.clock), func(t *testing.T) {
			a, srv, stub, put := newNGClockApp(t, "1.1.2")
			cacheRotation(t, srv)
			stub.orderCode = c.clock
			resp, b := put("date", `{"enabled":false}`)
			if resp.StatusCode != c.want {
				t.Fatalf("PUT = %d %s, want %d", resp.StatusCode, b, c.want)
			}
			if a.clockRotation.Load() == nil {
				t.Fatal("a clock refusal forgot the rotation")
			}
		})
	}
}

func TestAppEnabledFallbackSwitchesOffAnAppNotListedYet(t *testing.T) {
	_, _, stub, put := newNGClockApp(t, "1.1.2")
	mustPut(t, put, "app enabled", "weather", `{"enabled":false}`)
	puts := stub.puts()
	if len(puts) != 1 {
		t.Fatalf("order writes = %v", puts)
	}
	if body := decodeOrderPut(t, puts[0]); !slices.Equal(body.Disabled, []string{"hum", "weather"}) {
		t.Fatalf("disabled = %v, want [hum weather]", body.Disabled)
	}
}

func TestAppEnabledFallbackReadsAMissingEnabledAsOn(t *testing.T) {
	_, _, stub, put := newNGClockApp(t, "1.1.2")
	stub.mu.Lock()
	delete(stub.apps[1], "enabled")
	stub.mu.Unlock()
	mustPut(t, put, "app enabled", "time", `{"enabled":false}`)
	if body := decodeOrderPut(t, stub.puts()[0]); !slices.Equal(body.Disabled, []string{"time", "hum"}) {
		t.Fatalf("disabled = %v, want date (no enabled key) left on", body.Disabled)
	}
}

func TestAppEnabledRejectsBadNames(t *testing.T) {
	_, srv, stub, put := newNGClockApp(t, "1.1.2")
	stub.mu.Lock()
	stub.apps = append(stub.apps, map[string]any{"name": "mathlib", "origin": "module"})
	stub.mu.Unlock()
	for _, name := range []string{"order", "next", "previous", "active", "bad.name", strings.Repeat("a", 33), "mathlib"} {
		resp, b := put(name, `{"enabled":false}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("name %q = %d %s, want 400", name, resp.StatusCode, b)
		}
	}
	cacheRotation(t, srv)
	if resp, b := put("mathlib", `{"enabled":false}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("cached module = %d %s, want 400", resp.StatusCode, b)
	}
	if len(stub.puts()) != 0 {
		t.Fatalf("a bad name reached the order write: %v", stub.puts())
	}
	if got := stub.appEnabledPuts(); slices.ContainsFunc(got, func(p string) bool { return !strings.HasPrefix(p, "mathlib=") }) {
		t.Fatalf("a bad name reached the per-app route: %v", got)
	}
	mustPut(t, put, "valid name", "my_App-2", `{"enabled":true}`)
}

func TestAppEnabledRejectsBadBodies(t *testing.T) {
	_, _, stub, put := newNGClockApp(t, "1.2.2")
	for _, body := range []string{``, `{}`, `{"enabled":null}`, `{"enabled":"yes"}`, `true`, `{"enabled":true,"x":1}`} {
		resp, b := put("time", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q = %d %s, want 400", body, resp.StatusCode, b)
		}
	}
	if len(stub.appEnabledPuts()) != 0 || len(stub.puts()) != 0 {
		t.Fatalf("a rejected body reached the clock: %v %v", stub.appEnabledPuts(), stub.puts())
	}
}

func TestAppEnabledRefreshesTheFacadeRotation(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.version = "1.2.2"
	d := registeredClock(t, a, srv)
	_, v0 := getClockConfig(t, srv, d.ID)
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps/date/enabled", testToken, `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	after, v1 := getClockConfig(t, srv, d.ID)
	if after.Rotation == nil || !slices.Contains(after.Rotation.Disabled, "date") {
		t.Fatalf("facade rotation = %+v, want date off", after.Rotation)
	}
	if v1 <= v0 {
		t.Fatalf("config version %d -> %d, want a bump", v0, v1)
	}
}

func TestAppEnabledFallbackHoldsTheAppOrderLock(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.version = "1.1.2"
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	release := holdClockOrder(t, stub)
	first := clockGoReq(srv, "PUT", "/v1/device/apps/time/enabled", `{"enabled":false}`)
	waitEntered(t, stub.orderIn, "fallback order write")
	toggle := clockGoReq(srv, "PUT", "/v1/device/apps/hum/enabled", `{"enabled":true}`)
	facade := clockGoReq(srv, "PUT", "/v1/devices/"+d.ID+"/config", `{"apps":{"weather":{"moon":false}}}`)
	for what, ch := range map[string]<-chan reqReply{"second toggle": toggle, "facade put": facade} {
		if r, done := waitReply(ch, 200*time.Millisecond); done {
			t.Fatalf("%s finished (%d) while the fallback write held the lock", what, r.code)
		}
	}
	release()
	mustReply(t, "first toggle", first)
	mustReply(t, "second toggle", toggle)
	mustReply(t, "facade put", facade)
	if got := stub.enabledByName(); got["time"] != false || got["hum"] != true {
		t.Fatalf("clock apps = %v, want both toggles applied", got)
	}
	if r := a.clockRotation.Load(); r == nil || !slices.Equal(r.Disabled, []string{"time"}) {
		t.Fatalf("rotation = %+v, want only time off", r)
	}
	assertClockRecordInStep(t, a, srv)
}

func TestAppEnabledWaitsForAnOldRouteWrite(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.version = "1.2.2"
	d := registeredClock(t, a, srv)
	getClockConfig(t, srv, d.ID)
	in, release := stub.holdApps(t, http.StatusOK)
	legacy := clockGoReq(srv, "PUT", "/v1/device/apps", `{"order":["date","time"],"disabled":[]}`)
	waitEntered(t, in, "legacy read-back")
	toggle := clockGoReq(srv, "PUT", "/v1/device/apps/date/enabled", `{"enabled":false}`)
	if r, done := waitReply(toggle, 200*time.Millisecond); done {
		t.Fatalf("toggle finished (%d) during the legacy write's read-back", r.code)
	}
	if n := len(stub.appEnabledPuts()); n != 0 {
		t.Fatalf("toggle reached the clock while the lock was held: %v", stub.appEnabledPuts())
	}
	release()
	mustReply(t, "legacy", legacy)
	mustReply(t, "toggle", toggle)
	if r := a.clockRotation.Load(); r == nil || !slices.Contains(r.Disabled, "date") {
		t.Fatalf("rotation = %+v, want the toggle applied last", r)
	}
}

func TestAppEnabledExpiredBeforeLockIsRejected(t *testing.T) {
	a, _, stub, put := newNGClockApp(t, "1.1.2")
	a.rotationOp.budget.Store(0)
	for i := range 20 {
		if resp, b := put("date", `{"enabled":false}`); resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("try %d: PUT = %d %s, want 503", i, resp.StatusCode, b)
		}
	}
	if len(stub.appEnabledPuts()) != 0 || len(stub.puts()) != 0 {
		t.Fatalf("expired requests reached the clock: %v %v", stub.appEnabledPuts(), stub.puts())
	}
}

func TestVersionAdvertisesDeviceAppEnabled(t *testing.T) {
	if !slices.Contains(computeVersionInfo().Features, featureDeviceAppEnabled) {
		t.Fatalf("features = %v, want %s", computeVersionInfo().Features, featureDeviceAppEnabled)
	}
}

func TestAppEnabledRetriesThePerAppRouteAfterAReboot(t *testing.T) {
	a, _, stub, put := newNGClockApp(t, "1.2.2")
	stub.onStatus = http.StatusNotFound
	mustPut(t, put, "one-off 404", "date", `{"enabled":false}`)
	stub.mu.Lock()
	stub.onStatus = 0
	stub.mu.Unlock()
	mustPut(t, put, "still marked", "date", `{"enabled":true}`)
	if got := stub.appEnabledPuts(); len(got) != 1 {
		t.Fatalf("per-app writes before the reboot = %v, want 1", got)
	}
	stub.mu.Lock()
	stub.uptime = 5
	stub.mu.Unlock()
	if dev := a.probeClockHealthWithin(context.Background(), time.Now(), 0); dev == nil || !dev.Reachable {
		t.Fatalf("probe failed: %+v", dev)
	}
	mustPut(t, put, "after the reboot", "date", `{"enabled":false}`)
	if got := stub.appEnabledPuts(); len(got) != 2 || got[1] != "date=false" {
		t.Fatalf("per-app writes = %v, want a fresh try once uptime dropped", got)
	}
	if len(stub.puts()) != 2 {
		t.Fatalf("order writes = %v, want only the two while marked", stub.puts())
	}
}

func TestAppEnabledRetriesThePerAppRouteAfterAnHour(t *testing.T) {
	a, _, stub, put := newNGClockApp(t, "1.2.2")
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	a.appEnabled.mu.Lock()
	a.appEnabled.now = func() time.Time { return now }
	a.appEnabled.mu.Unlock()
	stub.onStatus = http.StatusNotFound
	mustPut(t, put, "one-off 404", "date", `{"enabled":false}`)
	stub.mu.Lock()
	stub.onStatus = 0
	stub.mu.Unlock()
	advance := func(d time.Duration) {
		a.appEnabled.mu.Lock()
		now = now.Add(d)
		a.appEnabled.mu.Unlock()
	}
	advance(appEnabledRetryAfter - time.Minute)
	mustPut(t, put, "within the hour", "date", `{"enabled":true}`)
	if got := stub.appEnabledPuts(); len(got) != 1 {
		t.Fatalf("per-app writes within the hour = %v, want 1", got)
	}
	advance(time.Minute)
	mustPut(t, put, "after the hour", "date", `{"enabled":false}`)
	if got := stub.appEnabledPuts(); len(got) != 2 || got[1] != "date=false" {
		t.Fatalf("per-app writes = %v, want a fresh try after the hour", got)
	}
}
