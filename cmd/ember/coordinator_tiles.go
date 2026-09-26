package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"
)

// adoptDeviceManagedApps seeds the in-memory push trackers from the apps
// actually present on the device, so ember-managed custom apps (weather,
// forecast, usage) left over from a previous process can be reconciled — and
// cleared when no longer wanted — even though the trackers start empty after a
// restart. Each adopted entry is seeded as stale (zero payload + time) so the
// normal reconcile re-pushes it if still desired, or clears it if not. The base
// rotating app and native apps (Time, etc.) are deliberately left untouched.
// Returns false if the device loop can't be read, so the caller retries on a
// later tick once the device is reachable. Runs on the coordinator goroutine.
func (c *coordinator) adoptDeviceManagedApps() bool {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	names, err := c.publisher.ListApps(ctx)
	if err != nil {
		c.logger.Warn("device app loop read failed; deferring adopt", "err", err)
		return false
	}
	baseApp := c.loadCfg().AWTRIX.AppName
	for _, name := range names {
		switch {
		case name == baseApp:
			// The main rotating app is owned by publish(), not the reconcilers.
		case name == "ember-weather":
			if c.pushedWeather == nil {
				c.pushedWeather = &pushedUsageApp{}
			}
		case name == "ember-forecast":
			if c.pushedForecast == nil {
				c.pushedForecast = &pushedUsageApp{}
			}
		case name == "ember-air":
			if c.pushedAir == nil {
				c.pushedAir = &pushedUsageApp{}
			}
		case name == "ember-meet":
			if c.pushedMeeting == nil {
				c.pushedMeeting = &pushedUsageApp{}
			}
		case strings.HasPrefix(name, "ember-usage-"):
			if c.pushedUsageApps == nil {
				c.pushedUsageApps = map[string]pushedUsageApp{}
			}
			if _, ok := c.pushedUsageApps[name]; !ok {
				c.pushedUsageApps[name] = pushedUsageApp{}
			}
		}
	}
	return true
}

// reconcileTile owns the shared clear/dedupe/push state machine for the
// standalone rotating tiles (weather/forecast/air/meeting). The per-tile
// decisions — whether the tile should be on the device and what it shows —
// stay at the call sites: want gates, buildPayload runs only when want is
// true. tracker is the per-tile *pushedUsageApp pointer-to-pointer so this
// helper can nil it on clear. Coordinator goroutine only.
func (c *coordinator) reconcileTile(now time.Time, name string, tracker **pushedUsageApp, want bool, buildPayload func() map[string]any) {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if !want {
		if *tracker != nil {
			if err := c.publisher.ClearApp(ctx, name); err != nil {
				c.logger.Warn("tile clear failed", "app", name, "err", err)
				return
			}
			*tracker = nil
		}
		return
	}
	payload := buildPayload()
	body, err := json.Marshal(payload)
	if err != nil {
		c.logger.Warn("tile payload marshal failed", "app", name, "err", err)
		return
	}
	if *tracker != nil && bytes.Equal((*tracker).body, body) && now.Sub((*tracker).at) < usageRefreshInterval {
		return
	}
	if err := c.pushApp(name, payload); err != nil {
		c.logger.Warn("tile publish failed", "app", name, "err", err)
		return
	}
	*tracker = &pushedUsageApp{body: body, at: now}
}

// pushedUsageApp records the payload bytes + push time of a usage app last sent
// to the device, for change-and-staleness-aware re-push.
type pushedUsageApp struct {
	body []byte
	at   time.Time
}
