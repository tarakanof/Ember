package main

import "time"

// retuneDwellTicker resets t to cfg's display.rotation_dwell_seconds when it
// differs from *cur, and records the new period in *cur. publish() reads the
// live dwell for its dedupe window, so a ticker left on the startup period
// after /admin/reload would drift away from it. A non-positive dwell falls
// back to the 3s default, as in StartCoordinator.
func retuneDwellTicker(t *time.Ticker, cur *time.Duration, cfg *Config) {
	d := time.Duration(cfg.Display.RotationDwellSeconds) * time.Second
	if d <= 0 {
		d = 3 * time.Second
	}
	if d != *cur {
		t.Reset(d)
		*cur = d
	}
}
