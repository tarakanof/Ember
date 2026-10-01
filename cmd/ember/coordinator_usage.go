package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	usageAppLifetime     = 600
	usageStaleTTL        = 10 * time.Minute
	usageRefreshInterval = 4 * time.Minute
)

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
