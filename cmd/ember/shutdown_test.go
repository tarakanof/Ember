package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// A container restart mid-focus must put the clock's rotation back before the
// process exits: shutdown has to wait for the coordinator's exit restore, and
// only then close the store.
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

	// Wait for the takeover to land (initial tick: push, settings, switch).
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

// slowRestorePublisher delays the restore PATCH like a real round trip to the
// clock, so a shutdown that doesn't wait for it returns first.
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

// A worker that ignores cancellation must not hang the exit past the deadline.
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
