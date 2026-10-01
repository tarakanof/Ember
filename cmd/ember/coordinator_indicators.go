package main

import (
	"context"
	"time"
)

const (
	indicatorRunningColor     = "#004000"
	indicatorWaitingColor     = "#FFA000"
	indicatorErrorColor       = "#FF0000"
	indicatorQuietColor       = "#000040"
	indicatorAttentionBlinkMs = 500
)

type indicatorState struct {
	color   string
	blinkMs int
}

func (c *coordinator) desiredIndicators(snap Snapshot, now time.Time) [3]indicatorState {
	var want [3]indicatorState
	cfg := c.loadCfg()
	if !cfg.Display.Indicators {
		return want
	}
	for _, s := range snap.Sessions {
		if s.State == "running" {
			want[0] = indicatorState{color: indicatorRunningColor}
			break
		}
	}
	if c.locked {
		for _, s := range snap.Sessions {
			if s.Key() != c.lockedKey {
				continue
			}
			switch s.State {
			case "waiting":
				want[1] = indicatorState{color: indicatorWaitingColor, blinkMs: indicatorAttentionBlinkMs}
			case "error":
				want[1] = indicatorState{color: indicatorErrorColor, blinkMs: indicatorAttentionBlinkMs}
			}
			break
		}
	}
	if enabled, start, end := cfg.quietHoursWindow(); enabled && quietActive(start, end, now) {
		want[2] = indicatorState{color: indicatorQuietColor}
	}
	return want
}

func (c *coordinator) applyIndicators(want [3]indicatorState) {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for i, w := range want {
		if w == c.indicators[i] {
			continue
		}
		var err error
		if w.color == "" {
			err = c.publisher.ClearIndicator(ctx, i+1)
		} else {
			err = c.publisher.Indicator(ctx, i+1, map[string]any{
				"color":   w.color,
				"blinkMs": w.blinkMs,
			})
		}
		if err != nil {
			c.logger.Warn("indicator write failed", "indicator", i+1, "err", err)
			continue
		}
		c.indicators[i] = w
	}
}
