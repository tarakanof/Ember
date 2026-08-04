package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newPublisherAgainst(t *testing.T, h http.HandlerFunc) *HTTPPublisher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv.URL
	cfg.applyDefaults()
	pub, err := NewHTTPPublisher()
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pub.app = app
	return pub
}

func TestPublisherSettingsPatchesSettingsEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})

	if err := pub.Settings(context.Background(), map[string]any{"appRotation": false}); err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/settings" {
		t.Fatalf("got %s %s, want PATCH /api/v1/settings", gotMethod, gotPath)
	}
	if gotBody["appRotation"] != false {
		t.Fatalf("body appRotation = %v, want false", gotBody["appRotation"])
	}
}

func TestPublisherSwitchPutsActiveApp(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})

	if err := pub.Switch(context.Background(), "ember"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/apps/active" {
		t.Fatalf("got %s %s, want PUT /api/v1/apps/active", gotMethod, gotPath)
	}
	if gotBody["name"] != "ember" {
		t.Fatalf("body name = %v, want ember", gotBody["name"])
	}
}

func TestPublisherCustomAppPutsPushedApp(t *testing.T) {
	var gotMethod, gotPath string
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := pub.CustomApp(context.Background(), "ember-weather", map[string]any{"text": "x"}); err != nil {
		t.Fatalf("CustomApp: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/apps/pushed/ember-weather" {
		t.Fatalf("got %s %s, want PUT /api/v1/apps/pushed/ember-weather", gotMethod, gotPath)
	}
}

func TestPublisherClearAppDeletes(t *testing.T) {
	var gotMethod, gotPath string
	var gotLen int64
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotLen = r.ContentLength
		w.WriteHeader(http.StatusOK)
	})
	if err := pub.ClearApp(context.Background(), "ember-weather"); err != nil {
		t.Fatalf("ClearApp: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/apps/ember-weather" {
		t.Fatalf("got %s %s, want DELETE /api/v1/apps/ember-weather", gotMethod, gotPath)
	}
	if gotLen > 0 {
		t.Fatalf("DELETE must have empty body, got %d bytes", gotLen)
	}
}

func TestPublisherListAppsParsesNGArray(t *testing.T) {
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps" {
			t.Errorf("path = %q, want /api/v1/apps", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Time","origin":"builtin"},{"name":"ember-usage-x","origin":"pushed"}]`))
	})
	names, err := pub.ListApps(context.Background())
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(names) != 2 || names[0] != "Time" || names[1] != "ember-usage-x" {
		t.Fatalf("names = %v", names)
	}
}

func TestPublisherDismissNotifyDeletesActive(t *testing.T) {
	var gotMethod, gotPath string
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := pub.DismissNotify(context.Background()); err != nil {
		t.Fatalf("DismissNotify: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/notifications/active" {
		t.Fatalf("got %s %s, want DELETE /api/v1/notifications/active", gotMethod, gotPath)
	}
}

func TestPublisherPlayRTTTLPostsJSONSoundsPlay(t *testing.T) {
	var gotPath, gotCT string
	var gotBody map[string]any
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})
	if err := pub.PlayRTTTL(context.Background(), "beep:d=16,o=6,b=140:c"); err != nil {
		t.Fatalf("PlayRTTTL: %v", err)
	}
	if gotPath != "/api/v1/sounds/play" || gotCT != "application/json" {
		t.Fatalf("got %s (%s), want /api/v1/sounds/play (application/json)", gotPath, gotCT)
	}
	if gotBody["rtttl"] != "beep:d=16,o=6,b=140:c" {
		t.Fatalf("body = %v", gotBody)
	}
}

func TestPublisherClearIndicatorDeletes(t *testing.T) {
	var gotMethod, gotPath string
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := pub.ClearIndicator(context.Background(), 2); err != nil {
		t.Fatalf("ClearIndicator: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/indicators/2" {
		t.Fatalf("got %s %s, want DELETE /api/v1/indicators/2", gotMethod, gotPath)
	}
}

// A 422 from the device must surface the offending field name so operators can
// see exactly which payload key NG rejected.
func TestPublisherSurfaces422Field(t *testing.T) {
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"validationFailed","message":"unknown key \"noScroll\"","field":"noScroll"}}`))
	})
	err := pub.CustomApp(context.Background(), "ember", map[string]any{"noScroll": true})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"422", "noScroll", "validationFailed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}
