package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestPublisherSettingsPostsToSettingsEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})

	if err := pub.Settings(context.Background(), map[string]any{"ATRANS": false, "BLOCKN": true}); err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if gotPath != "/api/settings" {
		t.Fatalf("path = %q, want /api/settings", gotPath)
	}
	if gotBody["BLOCKN"] != true {
		t.Fatalf("body BLOCKN = %v, want true", gotBody["BLOCKN"])
	}
}

func TestPublisherSwitchPostsAppName(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	pub := newPublisherAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})

	if err := pub.Switch(context.Background(), "ai_status"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if gotPath != "/api/switch" {
		t.Fatalf("path = %q, want /api/switch", gotPath)
	}
	if gotBody["name"] != "ai_status" {
		t.Fatalf("body name = %v, want ai_status", gotBody["name"])
	}
}
