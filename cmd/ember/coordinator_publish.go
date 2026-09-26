package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/render"
)

// publishAttemptTimeout bounds ONE pushed-app write, and publishAttempts is how
// many of them a frame gets before the coordinator gives up until the next tick.
//
// awtrix.timeout_seconds (10s by default) is the wrong budget here: it is the
// ceiling for any device call, while a frame push is a ~2.4 KB PUT to a device
// on the same LAN that answers in well under a second when the link is healthy
// (measured: 0.04s empty-ish, 0.55-0.68s at 3 KB). A push that has not answered
// in 2.5s has almost certainly been dropped, and every second spent waiting is a
// second the coordinator goroutine — which owns every device write — is not
// serving ticks, so its missed ticks turn into dropped state-change commands.
//
// Retrying inside the tick (rather than waiting a whole dwell for the next one)
// is what keeps a lossy link from evicting the app: the device drops a pushed
// app on its own lifetime, and it counts wallclock, not attempts.
const (
	publishAttemptTimeout = 2500 * time.Millisecond
	publishAttempts       = 2
)

// pushApp writes one pushed app, retrying a lost attempt within its own tick.
// Each attempt gets publishAttemptTimeout; a device that answers with an error
// (any *awtrix.APIError — a 422 rejection will not become a 200 on a retry) and
// a cancelled coordinator context both stop the loop immediately. Returns the
// last attempt's error. Coordinator goroutine only.
func (c *coordinator) pushApp(name string, payload map[string]any) error {
	return c.retryDevice(c.runCtx(), func(ctx context.Context) error {
		return c.publisher.CustomApp(ctx, name, payload)
	})
}

// retryDevice runs one device call with pushApp's retry policy: up to
// publishAttempts attempts of publishAttemptTimeout each, stopping early on
// success, on an answer a retry can't change (see retryablePushErr), or when
// ctx is done. Returns the last attempt's error.
func (c *coordinator) retryDevice(ctx context.Context, op func(context.Context) error) error {
	budget := publishAttemptTimeout
	if t := time.Duration(c.loadCfg().AWTRIX.TimeoutSeconds) * time.Second; t > 0 && t < budget {
		budget = t
	}
	var err error
	for i := 0; i < publishAttempts; i++ {
		if i > 0 {
			c.metrics.incPublishRetry()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, budget)
		err = op(attemptCtx)
		cancel()
		if err == nil || !retryablePushErr(err) || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// runCtx is the Run context, or Background before Run has started (tests that
// drive publish directly).
func (c *coordinator) runCtx() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// retryablePushErr reports whether a failed push is worth another attempt. A
// transport failure (timeout, refused, reset) carries no status and is exactly
// the lost-packet case retries exist for. A device that answered has decided:
// only 5xx and 429 can change on their own — this clock watchdog-resets and
// runs its HTTP server on the same task that drives the panel, so a 503 while
// busy is transient. Any other 4xx (422 on a payload NG rejects, 413 on one too
// large) will answer identically forever.
func retryablePushErr(err error) bool {
	var apiErr *awtrix.APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusTooManyRequests
}

// renewalDedupWindow returns how long an unchanged frame may be skipped before
// publish must re-push it, given the device-side lifetime and the tick cadence
// (both in seconds).
//
// The renewal margin it leaves is what a lossy link spends: the last tick before
// the window opens can land a full dwell early, so the wallclock slack before
// the device evicts the app is (margin - dwell). The original margin of one
// dwell + 1s left 1s of slack — a single attempt, so one dropped push took the
// app out of the device's rotation until the frame changed.
//
// A third of the lifetime is the target. The floor raises that for
// configurations where a third is too thin to fit one full pushApp budget plus
// the dwell jitter. On a lifetime so short that even the floor doesn't fit, the
// window bottoms out at 1s and every tick re-pushes: dedupe is device/network
// thrift, keeping the app alive is correctness, so the thrift is what gives.
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

// onRepublish forgets everything we believe the device is currently showing and
// runs a full cycle on the spot. Pushed apps are RAM-only on awtrix-ng: after a
// reboot the device holds none of them, while the dedupe caches below would
// happily suppress a re-push for a whole frame lifetime (and a tile whose
// content never changes would never come back at all). Dropping c.hold turns the
// next applyDisplayHold into a fresh edge, so an in-flight focus block or
// attention hold re-asserts its forced app switch (and, for Pomodoro, its
// autoTransition/blockNavigation) too. Coordinator goroutine only.
func (c *coordinator) onRepublish() {
	c.lastPayloadBytes = nil
	c.lastPublishedAt = time.Time{}
	// Tiles (and any legacy usage apps) died with the reboot.
	c.tiles.forget()
	c.hold = holdNone
	// A reboot also drops the corner LEDs, so forget them and let the cycle
	// below re-assert whatever the snapshot asks for.
	c.indicators = [3]indicatorState{}
	c.onTick()
}

func (c *coordinator) publish(snap Snapshot) {
	cfg := c.loadCfg()
	lifetime := cfg.Display.FrameLifetimeSeconds
	if lifetime < 5 {
		lifetime = 5 // floor below the validated min — keeps dedupWindow positive in low-lifetime test setups
	}
	idleRestore := time.Duration(cfg.Display.IdleRestoreSeconds) * time.Second
	now := c.clk.Now()

	// Ambient corner-LED status, from the same snapshot this frame renders.
	// Ahead of the frame work because it must run on every publish path,
	// including the dedupe skip and the nothing-to-show return below.
	c.applyIndicators(c.desiredIndicators(snap, now))

	// Pomodoro preempt (highest priority). An active timer owns the display:
	// render its frame and take the device over for the whole phase. When it
	// goes idle, fall through to the normal session rendering below, which may
	// itself want the (weaker) frame hold.
	var pomoActive bool
	var payload map[string]any
	// want is the device-level screen owner this frame asks for; it is applied
	// only once the payload is known to be on the device (see below).
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
			// pointer/cardCursor/locked are read without stateMu: publish runs only
			// on the coordinator goroutine, the only writer of this state.
			payload = render.RenderForCoord(snap, c.pointer, c.cardCursor, c.locked, lifetime, c.usageViews(now, snap))
			if render.AttentionHeld(snap, c.pointer, c.locked) {
				want = holdAttention
			}
		case idleModeDimmed:
			payload = render.RenderIdleFrame(lifetime)
		case idleModeOff:
			// Countdown elapsed. If a tool's 5h window is over the usage
			// threshold, keep the slot alive with the dimmed usage frame so a
			// hot window stays visible while the user is away. Otherwise let
			// the device's lifetime expire (AWTRIX returns to native apps).
			payload = render.RenderIdleUsagePayload(c.usageViews(now, snap), c.cardCursor, now, lifetime)
		}
	}
	if payload == nil {
		// Nothing to show — release the hold so the rotation (and, after a
		// Pomodoro takeover, the device's own settings) come back.
		c.applyDisplayHold(holdNone, cfg.AWTRIX.AppName)
		return
	}

	body, mErr := json.Marshal(payload)
	if mErr != nil {
		c.logger.Error("coord payload marshal failed", "err", mErr)
		return
	}

	// Skip identical re-publishes within the dedup window.
	//
	// The original reason — AWTRIX3 reset a re-POSTed app's render state, so an
	// unchanged re-push restarted the blinking label mid-cycle as a visible
	// stutter — does NOT apply to awtrix-ng. Measured on firmware 1.0.13: 20
	// re-pushes of a byte-identical textBlinkMs:1000 payload over 6 s (i.e. more
	// often than the 500 ms half-period) left the blink alternating on its
	// original phase, and an app re-pushed every 2 s yielded its slot at the same
	// ~8.7 s dwell as one pushed once. A re-push is idempotent for both animation
	// phase and dwell timing.
	//
	// It stays because the work it avoids is real: an unchanged frame otherwise
	// costs a JSON push (up to ~2.4 KB of bitmap) to the ESP32 on every rotation
	// tick, parsed on the same task that drives the panel.
	//
	dwellSec := cfg.Display.RotationDwellSeconds
	if dwellSec <= 0 {
		dwellSec = 3
	}
	dedupWindow := renewalDedupWindow(lifetime, dwellSec)
	if bytes.Equal(body, c.lastPayloadBytes) && now.Sub(c.lastPublishedAt) < dedupWindow {
		// Same frame, already on the device: the hold edge may still be new
		// (e.g. a Pomodoro pause that leaves the payload byte-identical).
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
		c.lastPayloadBytes = body
		c.lastPublishedAt = now
		// Only now is the app known to be in the device's loop — apps/active
		// 404s on an app the device does not have.
		c.applyDisplayHold(want, cfg.AWTRIX.AppName)
	}
	if c.onPublishResult != nil {
		c.onPublishResult(snap, err)
	}
}
