package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestUpdateConfigConcurrentPUTsBothSurvive drives two goroutines that each
// repeatedly PUT a different config section (display, quiet hours) via the
// HTTP appliers, while a third goroutine polls the live config. Each applier
// does an unsynchronized read-copy-write on the shared atomic.Pointer[Config]
// (cur := *a.cfg.Load(); cur.X = ...; a.cfg.Store(&cur)): if goroutine B reads
// its copy before goroutine A's store lands, B's own store can silently
// revert A's change back to the pre-PUT default. Once a section's live value
// has been observed at its PUT target, it must never again be observed back
// at its startup default — that reversion is the lost-update bug this test
// targets. Run with -race.
func TestUpdateConfigConcurrentPUTsBothSurvive(t *testing.T) {
	a := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())

	const iterations = 20000
	const defaultIdleRestoreSeconds = 120 // from defaultConfig(); differs from the target below

	displayTarget := displayConfigDTO{IdleHideMinutes: 5, AttentionHoldSeconds: 45, AttentionChime: true}
	quietTarget := quietConfigDTO{Enabled: true, Start: "23:00", End: "07:00"}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			a.applyDisplaySettings(displayTarget)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			a.applyQuietSettings(quietTarget)
		}
	}()

	var sawDisplayTarget, sawQuietTarget atomic.Bool
	var displayReverted, quietReverted atomic.Bool
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			cfg := a.cfg.Load()
			if cfg.Display.IdleRestoreSeconds == displayTarget.IdleHideMinutes*60 {
				sawDisplayTarget.Store(true)
			} else if cfg.Display.IdleRestoreSeconds == defaultIdleRestoreSeconds && sawDisplayTarget.Load() {
				displayReverted.Store(true)
			}
			if cfg.QuietHours.Enabled && cfg.QuietHours.Start == quietTarget.Start {
				sawQuietTarget.Store(true)
			} else if !cfg.QuietHours.Enabled && cfg.QuietHours.Start == "" && sawQuietTarget.Load() {
				quietReverted.Store(true)
			}
		}
	}()

	wg.Wait()
	close(stop)
	// Give the poller a moment to observe the final settled state too.
	time.Sleep(time.Millisecond)

	if displayReverted.Load() {
		t.Error("display settings were reverted to the startup default by a concurrent quiet-hours write")
	}
	if quietReverted.Load() {
		t.Error("quiet-hours settings were reverted to the startup default by a concurrent display write")
	}

	got := a.cfg.Load()
	if got.Display.IdleRestoreSeconds != displayTarget.IdleHideMinutes*60 || got.Display.AckTimeoutSeconds != displayTarget.AttentionHoldSeconds || got.Display.AttentionChime != displayTarget.AttentionChime {
		t.Fatalf("final display settings not applied: %+v", got.Display)
	}
	if !got.QuietHours.Enabled || got.QuietHours.Start != quietTarget.Start || got.QuietHours.End != quietTarget.End {
		t.Fatalf("final quiet-hours settings not applied: %+v", got.QuietHours)
	}
}
