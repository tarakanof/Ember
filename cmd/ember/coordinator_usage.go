package main

import (
	"context"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	// usageAppLifetime keeps a pushed usage app alive on the device well above
	// the ~5-min producer refresh, so a brief reconcile gap never blanks it.
	usageAppLifetime = 600 // seconds
	// usageStaleTTL is ~2x the 5-min poll interval: past this with no fresh
	// post, a tool's apps are cleared from the device.
	usageStaleTTL = 10 * time.Minute
	// usageRefreshInterval forces a re-push of an unchanged usage app well
	// before its on-device lifetime (usageAppLifetime) expires — otherwise a
	// usage value that stops changing would let the device evict the app and
	// never get refreshed. Must be < usageAppLifetime.
	usageRefreshInterval = 4 * time.Minute
)

// pctInt rounds a float utilization to the nearest int, clamped to 0..100.
func pctInt(f float64) int {
	n := int(f + 0.5)
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// usageViews builds the per-tool usage views the render layer consumes:
// endpoint usage preferred, statusline fallback (same precedence as the
// limit alarm via effectiveFiveHour), gated at usage_threshold_pct. Hidden
// tools and below-threshold tools are absent. Returns nil when the widget
// is off or no store is wired.
func (c *coordinator) usageViews(now time.Time, snap Snapshot) map[string]*render.UsageView {
	cfg := c.loadCfg()
	if c.usage == nil || !cfg.usageWidgetEnabled() {
		return nil
	}
	thr := cfg.usageThresholdPct()
	var hidden map[string]bool
	if c.hiddenApps != nil {
		hidden = c.hiddenApps()
	}
	views := map[string]*render.UsageView{}
	for _, tool := range []string{"claude", "codex"} {
		if hidden[tool] {
			continue
		}
		pct, resetAt, ok := effectiveFiveHour(c.usage, snap, tool, now)
		if !ok || pctInt(pct) < thr {
			continue
		}
		v := &render.UsageView{FiveHourPct: pctInt(pct), ResetAt: resetAt}
		if c.usage.Fresh(tool, now, usageStaleTTL) {
			u, _ := c.usage.Get(tool)
			if u.FiveHour != nil {
				v.ResetLabel = u.FiveHour.ResetLabel
			}
			if u.SevenDay != nil {
				p := pctInt(u.SevenDay.UsedPercent)
				v.SevenDayPct = &p
			}
			if cfg.usagePerModelEnabled() {
				for _, m := range []string{"opus", "sonnet"} {
					if w := u.Models[m]; w != nil {
						marker := "OP"
						if m == "sonnet" {
							marker = "SO"
						}
						v.Models = append(v.Models, render.ModelUsage{Marker: marker, Pct: pctInt(w.UsedPercent)})
					}
				}
			}
		} else {
			// Statusline fallback: the newest live session's host-local label.
			// effectiveFiveHour already accepted a session, but didn't give us
			// the label — find the same best session to populate ResetLabel.
			var best *render.Session
			for i := range snap.Sessions {
				s := &snap.Sessions[i]
				if s.Tool != tool || s.RateResetLabel == "" {
					continue
				}
				if best == nil || s.UpdatedAt.After(best.UpdatedAt) {
					best = s
				}
			}
			if best != nil {
				v.ResetLabel = best.RateResetLabel
			}
		}
		views[tool] = v
	}
	return views
}

// clearLegacyUsageApps removes any standalone ember-usage-* apps from the
// device. Usage now renders inside the main app (usage card / idle usage
// frame); the only standalone apps left to handle are leftovers from an
// older server version, seeded into pushedUsageApps by
// adoptDeviceManagedApps. Failed clears stay tracked and retry next tick.
func (c *coordinator) clearLegacyUsageApps() {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for name := range c.pushedUsageApps {
		if err := c.publisher.ClearApp(ctx, name); err != nil {
			c.logger.Warn("legacy usage app clear failed", "name", name, "err", err)
			continue
		}
		delete(c.pushedUsageApps, name)
	}
}
