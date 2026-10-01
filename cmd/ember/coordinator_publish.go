package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func (c *coordinator) pushApp(name string, payload map[string]any) error {
	return c.retryDevice(c.runCtx(), func(ctx context.Context) error {
		return c.publisher.CustomApp(ctx, name, payload)
	})
}

func (c *coordinator) retryDevice(ctx context.Context, op func(context.Context) error) error {
	budget := publishAttemptTimeout
	if t := time.Duration(c.loadCfg().AWTRIX.TimeoutSeconds) * time.Second; t > 0 && t < budget {
		budget = t
	}
	return retryClockCall(ctx, budget, c.metrics.incPublishRetry, op)
}

func (c *coordinator) runCtx() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func renewalDedupWindow(lifetimeSec, dwellSec int) time.Duration {
	margin := lifetimeSec / 3
	if floor := dwellSec + int(publishAttempts*publishAttemptTimeout/time.Second) + 1; margin < floor {
		margin = floor
	}
	window := time.Duration(lifetimeSec-margin) * time.Second
	if window < time.Second {
		window = time.Second
	}
	return window
}

func (c *coordinator) onRepublish() {
	c.mainPushed = pushedApp{}
	c.tiles.forget()
	c.hold = holdNone
	c.indicators = [3]indicatorState{}
	c.onTick()
}

func (c *coordinator) publish(snap Snapshot) {
	cfg := c.loadCfg()
	lifetime := cfg.Display.FrameLifetimeSeconds
	if lifetime < 5 {
		lifetime = 5
	}
	idleRestore := time.Duration(cfg.Display.IdleRestoreSeconds) * time.Second
	now := c.clk.Now()

	c.applyIndicators(c.desiredIndicators(snap, now))

	var pomoActive bool
	var payload map[string]any
	want := holdNone
	if c.pomoView != nil {
		if view, on := c.pomoView(); on {
			pomoActive = true
			payload = render.PomodoroPayload(view, lifetime)
			want = holdPomodoro
		}
	}

	if !pomoActive {
		keys := render.SortedActiveKeys(snap)
		c.stateMu.Lock()
		mode := c.idleStateLocked(len(keys), now, idleRestore)
		c.stateMu.Unlock()

		switch mode {
		case idleModeActive:
			payload = render.RenderForCoord(snap, c.pointer, c.cardCursor, c.locked, lifetime, c.usageViews(now, snap))
			if render.AttentionHeld(snap, c.pointer, c.locked) {
				want = holdAttention
			}
		case idleModeDimmed:
			payload = render.RenderIdleFrame(lifetime)
		case idleModeOff:
			payload = render.RenderIdleUsagePayload(c.usageViews(now, snap), c.cardCursor, now, lifetime)
		}
	}
	if payload == nil {
		c.applyDisplayHold(holdNone, cfg.AWTRIX.AppName)
		return
	}

	body, mErr := json.Marshal(payload)
	if mErr != nil {
		c.logger.Error("coord payload marshal failed", "err", mErr)
		return
	}

	dwellSec := cfg.Display.RotationDwellSeconds
	if dwellSec <= 0 {
		dwellSec = 3
	}
	dedupWindow := renewalDedupWindow(lifetime, dwellSec)
	if c.mainPushed.current(body, now, dedupWindow) {
		c.applyDisplayHold(want, cfg.AWTRIX.AppName)
		return
	}

	err := c.pushApp(cfg.AWTRIX.AppName, payload)
	if err != nil {
		c.logger.Warn("coord publish failed", "err", err)
		c.metrics.incPublishFail()
	} else {
		c.publishCount.Add(1)
		c.metrics.incPublishOK()
		c.mainPushed = pushedApp{body: body, at: now}
		c.applyDisplayHold(want, cfg.AWTRIX.AppName)
	}
	if c.onPublishResult != nil {
		c.onPublishResult(snap, err)
	}
}
