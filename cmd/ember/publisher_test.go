package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPublisher_BaseURLReloadable(t *testing.T) {
	hits1, hits2 := 0, 0
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits1++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv1.URL
	cfg.applyDefaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub, err := NewHTTPPublisher() // app filled below
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, pub, logger)
	pub.app = app

	if err := pub.Notify(context.Background(), map[string]any{"text": "x"}); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if hits1 != 1 || hits2 != 0 {
		t.Errorf("after publish 1: hits1=%d hits2=%d, want 1/0", hits1, hits2)
	}

	// Swap cfg to point at srv2.
	newCfg := *app.cfg.Load()
	newCfg.AWTRIX.HTTPBaseURL = srv2.URL
	app.cfg.Store(&newCfg)

	if err := pub.Notify(context.Background(), map[string]any{"text": "y"}); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if hits1 != 1 || hits2 != 1 {
		t.Errorf("after publish 2: hits1=%d hits2=%d, want 1/1", hits1, hits2)
	}
}
