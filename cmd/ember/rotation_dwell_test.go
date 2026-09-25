package main

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestRetuneDwellTickerAppliesReloadedDwell asserts a changed
// rotation_dwell_seconds retunes the running ticker instead of leaving it on
// the startup period.
func TestRetuneDwellTickerAppliesReloadedDwell(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := defaultConfig()
		cfg.Display.RotationDwellSeconds = 10
		dwell := 10 * time.Second
		tk := time.NewTicker(dwell)
		defer tk.Stop()

		cfg.Display.RotationDwellSeconds = 2
		retuneDwellTicker(tk, &dwell, &cfg)

		start := time.Now()
		<-tk.C
		if got := time.Since(start); got != 2*time.Second {
			t.Errorf("next tick after %v, want 2s", got)
		}
		if dwell != 2*time.Second {
			t.Errorf("tracked dwell = %v, want 2s", dwell)
		}
	})
}

// TestRetuneDwellTickerDefaultsNonPositive asserts a zero dwell falls back
// to the 3s default rather than resetting the ticker to an invalid period.
func TestRetuneDwellTickerDefaultsNonPositive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := defaultConfig()
		cfg.Display.RotationDwellSeconds = 0
		dwell := 5 * time.Second
		tk := time.NewTicker(dwell)
		defer tk.Stop()

		retuneDwellTicker(tk, &dwell, &cfg)
		if dwell != 3*time.Second {
			t.Errorf("tracked dwell = %v, want the 3s default", dwell)
		}
	})
}
