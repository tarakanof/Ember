package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func newNGClockApp(t *testing.T, version string) (*App, *clockStub, func(name, body string) (*http.Response, []byte)) {
	t.Helper()
	a, srv, stub := newClockApp(t)
	stub.version = version
	a.refreshCapabilities(context.Background())
	put := func(name, body string) (*http.Response, []byte) {
		t.Helper()
		return devReq(t, srv, "PUT", "/v1/device/apps/"+name+"/enabled", testToken, body)
	}
	return a, stub, put
}

func decodeOrderPut(t *testing.T, raw string) deviceAppsPutBody {
	t.Helper()
	var b deviceAppsPutBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("order body %q: %v", raw, err)
	}
	return b
}

func TestAppEnabledUsesPerAppRouteOnNG12(t *testing.T) {
	_, stub, put := newNGClockApp(t, "1.2.2")
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
	_, stub, put := newNGClockApp(t, "1.2.2")
	stub.mu.Lock()
	stub.apps[2]["enabled"] = true
	stub.mu.Unlock()
	resp, b := put("date", `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	if got := stub.enabledByName(); got["hum"] != true || got["date"] != false {
		t.Fatalf("clock apps = %v, want hum left on as set elsewhere, date off", got)
	}
}

func TestAppEnabledFallsBackToFreshOrderWriteBeforeNG12(t *testing.T) {
	for _, version := range []string{"1.0.13", "1.1.2", "1.2.0-beta1", "garbage"} {
		t.Run(version, func(t *testing.T) {
			_, stub, put := newNGClockApp(t, version)
			stub.mu.Lock()
			stub.apps[1]["enabled"] = false
			stub.mu.Unlock()
			resp, b := put("hum", `{"enabled":true}`)
			mustOK(t, "app enabled", resp, b)
			if got := stub.appEnabledPuts(); len(got) != 0 {
				t.Fatalf("firmware %s got per-app writes %v; it lacks the route", version, got)
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

func TestAppEnabledFallsBackWhenUnknownFirmware(t *testing.T) {
	a, srv, stub := newClockApp(t)
	stub.version = "1.2.2"
	if v := a.deviceFirmware(); v != "" {
		t.Fatalf("firmware cached before any probe: %q", v)
	}
	resp, b := devReq(t, srv, "PUT", "/v1/device/apps/time/enabled", testToken, `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	if got := stub.appEnabledPuts(); len(got) != 0 {
		t.Fatalf("unknown firmware got per-app writes %v", got)
	}
	if len(stub.puts()) != 1 {
		t.Fatalf("order writes = %v, want one", stub.puts())
	}
	if got := stub.enabledByName(); got["time"] != false || got["hum"] != false {
		t.Fatalf("clock apps = %v", got)
	}
}

func TestAppEnabledFallsBackWhenClockLacksTheRoute(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			_, stub, put := newNGClockApp(t, "1.2.2")
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
			body := decodeOrderPut(t, puts[0])
			if !slices.Equal(body.Disabled, []string{"date", "hum"}) {
				t.Fatalf("fallback disabled = %v, want [date hum]", body.Disabled)
			}
		})
	}
}

func TestAppEnabledSurfacesOtherClockRejections(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusInsufficientStorage} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			_, stub, put := newNGClockApp(t, "1.2.2")
			stub.onStatus = status
			resp, b := put("date", `{"enabled":false}`)
			if resp.StatusCode != deviceProxyStatus(status) || !strings.Contains(string(b), "clock returned") {
				t.Fatalf("PUT = %d %s, want %d relaying the clock", resp.StatusCode, b, deviceProxyStatus(status))
			}
			if len(stub.puts()) != 0 {
				t.Fatalf("a refused toggle fell back to an order write: %v", stub.puts())
			}
		})
	}
}

func TestAppEnabledFallbackSwitchesOffAnAppNotListedYet(t *testing.T) {
	_, stub, put := newNGClockApp(t, "1.1.2")
	resp, b := put("weather", `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	puts := stub.puts()
	if len(puts) != 1 {
		t.Fatalf("order writes = %v", puts)
	}
	if body := decodeOrderPut(t, puts[0]); !slices.Equal(body.Disabled, []string{"hum", "weather"}) {
		t.Fatalf("disabled = %v, want [hum weather]", body.Disabled)
	}
}

func TestAppEnabledFallbackSkipsModules(t *testing.T) {
	_, stub, put := newNGClockApp(t, "1.1.2")
	stub.mu.Lock()
	stub.apps = append(stub.apps, map[string]any{"name": "mathlib", "origin": "module"})
	stub.mu.Unlock()
	resp, b := put("time", `{"enabled":false}`)
	mustOK(t, "app enabled", resp, b)
	body := decodeOrderPut(t, stub.puts()[0])
	if slices.Contains(body.Order, "mathlib") || slices.Contains(body.Disabled, "mathlib") {
		t.Fatalf("order write names a module: %+v", body)
	}
}

func TestAppEnabledRejectsBadBodies(t *testing.T) {
	_, stub, put := newNGClockApp(t, "1.2.2")
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
	a.refreshCapabilities(context.Background())
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

func TestVersionAdvertisesDeviceAppEnabled(t *testing.T) {
	if !slices.Contains(computeVersionInfo().Features, featureDeviceAppEnabled) {
		t.Fatalf("features = %v, want %s", computeVersionInfo().Features, featureDeviceAppEnabled)
	}
}

func TestClockHasAppEnabled(t *testing.T) {
	cases := map[string]bool{
		"1.2.0": true, "1.2.2": true, "1.10.0": true, "2.0.0": true,
		"1.1.9": false, "1.0.13": false, "": false, "1.2.0-beta1": false, "x": false,
	}
	for v, want := range cases {
		if got := clockHasAppEnabled(v); got != want {
			t.Errorf("clockHasAppEnabled(%q) = %v, want %v", v, got, want)
		}
	}
}
