package main

import "time"

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
