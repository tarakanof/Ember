package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestShutdownWaitsForTakeoverRestore(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.applyDefaults()
	a := NewApp(cfg, &slowRestorePublisher{pub}, testLogger())
	if err := a.ensureStore(t.TempDir() + "/s.db"); err != nil {
		t.Fatal(err)
	}
	a.coord.pomoView = func() (render.PomodoroView, bool) {
		return render.PomodoroView{Phase: "focus", RemainingSec: 1500, PlannedSec: 1500}, true
	}

	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Go(func() { a.StartCoordinator(ctx) })

	deadline := time.Now().Add(5 * time.Second)
	for len(pub.SwitchesSnapshot()) == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("takeover never applied")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	a.shutdown(shutdownCtx, &http.Server{}, &workers)

	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings when shutdown returned = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[1], true, false)
	if err := a.store.PutSetting("k", "v"); err == nil {
		t.Fatal("store still open after shutdown")
	}
}

type slowRestorePublisher struct{ *recordingPublisher }

func (p *slowRestorePublisher) Settings(ctx context.Context, payload map[string]any) error {
	if payload["autoTransition"] == true {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.recordingPublisher.Settings(ctx, payload)
}

func TestShutdownIsBoundedByItsDeadline(t *testing.T) {
	a := newTestAppWithStore(t)
	release := make(chan struct{})
	defer close(release)
	var workers sync.WaitGroup
	workers.Go(func() { <-release })

	ctx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	done := make(chan struct{})
	go func() {
		a.shutdown(ctx, &http.Server{}, &workers)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown blocked on a stuck worker past its deadline")
	}
}

func TestShutdownCancelsStalledIconJob(t *testing.T) {
	clock := &iconClockStub{fingerprint: true, stallList: make(chan struct{})}
	clockSrv := clock.server(t)
	a := iconClockApp(t, clockSrv.URL)
	t.Cleanup(func() { close(clock.stallList) })
	if err := a.ensureStore(t.TempDir() + "/s.db"); err != nil {
		t.Fatal(err)
	}
	a.provisionIconsInBackground()
	waitFor(t, "icon list request", func() bool {
		return len(clock.requests("GET /api/v1/files")) > 0
	})

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	start := time.Now()
	a.shutdown(ctx, &http.Server{}, &sync.WaitGroup{})
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("shutdown took %v behind a stalled icon job", took)
	}
	assertMigrationStopped(t, a)
}

func TestShutdownStopsMigrationBehindStalledClockJob(t *testing.T) {
	a := newTestAppWithStore(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a.clockJobs.Go(func() { <-release })

	ctx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	a.shutdown(ctx, &http.Server{}, &sync.WaitGroup{})
	assertMigrationStopped(t, a)
}

func assertMigrationStopped(t *testing.T, a *App) {
	t.Helper()
	ran := make(chan struct{})
	a.clockMigrate.start(func() { close(ran) })
	select {
	case <-ran:
		t.Fatal("a clock migration started after shutdown: the migration was never stopped")
	case <-time.After(100 * time.Millisecond):
	}
}
