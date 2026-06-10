package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

func newTestAppWithStore(t *testing.T) *App {
	t.Helper()
	a := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if err := a.ensureStore(t.TempDir() + "/s.db"); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDeviceConfigSourcePrecedence(t *testing.T) {
	a := newTestAppWithStore(t)
	a.deviceBaseline = "http://192.168.0.14"
	cur := *a.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = "http://192.168.0.14"
	a.cfg.Store(&cur)

	if got := a.deviceSource(); got != "config" {
		t.Fatalf("source=%q want config", got)
	}
	if err := a.applyDeviceBaseURL("http://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if got := a.deviceSource(); got != "store" {
		t.Fatalf("source=%q want store", got)
	}
	if got := a.cfg.Load().AWTRIX.HTTPBaseURL; got != "http://10.0.0.5" {
		t.Fatalf("effective url=%q", got)
	}
}

func TestDeviceSourceDiscoveredAndNone(t *testing.T) {
	a := newTestAppWithStore(t)
	a.deviceBaseline = "" // config.json had nothing
	cur := *a.cfg.Load()
	cur.AWTRIX.HTTPBaseURL = "http://10.0.0.9" // came from discovery
	a.cfg.Store(&cur)
	if got := a.deviceSource(); got != "discovered" {
		t.Fatalf("source=%q want discovered", got)
	}
	cur.AWTRIX.HTTPBaseURL = ""
	a.cfg.Store(&cur)
	if got := a.deviceSource(); got != "none" {
		t.Fatalf("source=%q want none", got)
	}
}

func TestDeviceConfigPutValidatesURL(t *testing.T) {
	a := newTestAppWithStore(t)
	r := httptest.NewRequest("PUT", "/v1/device/config", strings.NewReader(`{"base_url":"not a url"}`))
	w := httptest.NewRecorder()
	a.handleDeviceConfigPut(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestDeviceConfigPutPersists(t *testing.T) {
	a := newTestAppWithStore(t)
	r := httptest.NewRequest("PUT", "/v1/device/config", strings.NewReader(`{"base_url":"http://10.0.0.7"}`))
	w := httptest.NewRecorder()
	a.handleDeviceConfigPut(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "store") {
		t.Fatalf("put: code=%d body=%s", w.Code, w.Body.String())
	}
	if v, ok, _ := a.store.GetSetting(deviceBaseURLKey); !ok || v != "http://10.0.0.7" {
		t.Fatalf("persisted=%q ok=%v", v, ok)
	}
}

func TestDeviceDiscoverUsesInjectedBrowse(t *testing.T) {
	a := newTestAppWithStore(t)
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{Host: "awtrix.local.", BaseURL: "http://10.0.0.9", UID: "u", Version: "0.98"}}, nil
	}
	r := httptest.NewRequest("GET", "/v1/device/discover", nil)
	w := httptest.NewRecorder()
	a.handleDeviceDiscover(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "10.0.0.9") {
		t.Fatalf("discover: code=%d body=%s", w.Code, w.Body.String())
	}
}
