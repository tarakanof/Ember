package main

import (
	"context"
	"time"
)

// The three corner LEDs carry ambient status that survives whatever frame is on
// the matrix: 1 = a session is running, 2 = attention is being held, 3 = quiet
// hours. Colours are deliberately dim — these sit next to the clock all day.
const (
	indicatorRunningColor = "#004000" // dim green
	indicatorWaitingColor = "#FFA000" // amber
	indicatorErrorColor   = "#FF0000" // red
	indicatorQuietColor   = "#000040" // dim blue
	// indicatorAttentionBlinkMs is NG's 50%-duty blink period for indicator 2.
	indicatorAttentionBlinkMs = 500
)

// indicatorState is the desired state of one corner LED. The zero value is
// "off", which is written as DELETE /api/v1/indicators/{id} — NG keeps blinkMs
// and the stored colour on a PUT, so only a DELETE truly resets an LED.
type indicatorState struct {
	color   string
	blinkMs int
}

// desiredIndicators maps the snapshot the coordinator is about to render onto
// the three corner LEDs. All-off when display.indicators is unset, so the opt-in
// flag also turns them back off (via the change detection in applyIndicators).
// Reads coordinator-owned lock state, so coordinator goroutine only.
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
		// Attention follows the LOCK, not any waiting session: the lock is what
		// is actually holding the screen, and it releases on ack timeout.
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

// applyIndicators writes only the LEDs whose desired state changed, so the
// steady state costs no device traffic at all — the alternative is three extra
// HTTP calls on every dwell tick. A failed write is not recorded as applied, so
// the next publish retries it. Coordinator goroutine only.
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
