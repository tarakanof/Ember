package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	usageAppLifetime     = 600
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
	state := c.usage.state(snap.Sessions, now)
	views := map[string]*render.UsageView{}
	for _, tool := range usageTools {
		if hidden[tool] {
			continue
		}
		t := state.Tools[tool]
		if !t.HaveFiveHour || pctInt(t.FiveHourPct) < thr {
			continue
		}
		v := &render.UsageView{FiveHourPct: pctInt(t.FiveHourPct), ResetAt: t.ResetAt, ResetLabel: t.ResetLabel}
		if t.Fresh {
			if t.Report.SevenDay != nil {
				p := pctInt(t.Report.SevenDay.UsedPercent)
				v.SevenDayPct = &p
			}
			if cfg.usagePerModelEnabled() {
				for _, m := range []string{"opus", "sonnet"} {
					if w := t.Report.Models[m]; w != nil {
						marker := "OP"
						if m == "sonnet" {
							marker = "SO"
						}
						v.Models = append(v.Models, render.ModelUsage{Marker: marker, Pct: pctInt(w.UsedPercent)})
					}
				}
			}
		}
		views[tool] = v
	}
	return views
}
