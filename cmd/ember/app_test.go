package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestApp_PublishUpdatesLastPublishFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv.URL
	cfg.applyDefaults()

	app := NewApp(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Seed a running session so RenderForCoord produces a non-nil payload.
	app.Upsert(StatusRequest{Source: "dt", Tool: "claude", Session: "s1", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.coord.Run(ctx)
	app.coord.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.lastPublishAt.IsZero() {
		t.Error("lastPublishAt not updated")
	}
	if !app.lastPublishOK {
		t.Errorf("lastPublishOK = false, want true (err=%q)", app.lastPublishErr)
	}
	if app.lastPublishErr != "" {
		t.Errorf("lastPublishErr = %q, want empty", app.lastPublishErr)
	}
}

func TestIndicatorsOffOnStartup(t *testing.T) {
	cfg := defaultConfig()
	publisher := &recordingPublisher{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := NewApp(cfg, publisher, logger)

	if err := app.ClearIndicators(context.Background()); err != nil {
		t.Fatalf("ClearIndicators: %v", err)
	}

	if len(publisher.clearedIndicators) != 3 {
		t.Fatalf("cleared indicators = %d, want 3 (all off)", len(publisher.clearedIndicators))
	}
	for i, idx := range publisher.clearedIndicators {
		if idx != i+1 {
			t.Errorf("cleared indicator %d = %d, want %d", i, idx, i+1)
		}
	}
}
