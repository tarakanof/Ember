package main

import (
	"context"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	limitAlarmThreshold = 99.5
	limitAlarmGraceSec  = 60
	limitAlarmPopupSec  = 10
	limitAlarmRTTTL     = "reset:d=8,o=6,b=160:g,8p,c7,8p,e7"
)

func effectiveFiveHour(st *UsageStore, snap Snapshot, tool string, now time.Time) (pct float64, resetAt int64, ok bool) {
	if st != nil && st.Fresh(tool, now, usageStaleTTL) {
		if u, _ := st.Get(tool); u.FiveHour != nil {
			return u.FiveHour.UsedPercent, u.FiveHour.ResetsAt, true
		}
	}
	var best *render.Session
	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		if s.Tool != tool || s.RateWindowPct == nil || s.RateResetAt == 0 {
			continue
		}
		if best == nil || s.UpdatedAt.After(best.UpdatedAt) {
			best = s
		}
	}
	if best == nil {
		return 0, 0, false
	}
	return float64(*best.RateWindowPct), best.RateResetAt, true
}

func (c *coordinator) checkLimitAlarms(now time.Time, snap Snapshot) {
	if c.usage == nil || !c.loadCfg().limitAlarmEnabled() {
		c.alarmArmed = nil
		return
	}
	if c.alarmArmed == nil {
		c.alarmArmed = map[string]int64{}
		c.alarmFired = map[string]int64{}
	}
	for _, tool := range []string{"claude", "codex"} {
		pct, resetAt, ok := effectiveFiveHour(c.usage, snap, tool, now)
		if ok && pct >= limitAlarmThreshold && resetAt > now.Unix() && c.alarmFired[tool] != resetAt {
			c.alarmArmed[tool] = resetAt
		}
		armed, isArmed := c.alarmArmed[tool]
		if !isArmed || now.Unix() < armed+limitAlarmGraceSec {
			continue
		}
		if ok && pct >= limitAlarmThreshold && resetAt > armed {
			c.alarmArmed[tool] = resetAt
			continue
		}
		if err := c.fireLimitAlarm(tool); err != nil {
			continue
		}
		c.alarmFired[tool] = armed
		delete(c.alarmArmed, tool)
	}
}

func (c *coordinator) fireLimitAlarm(tool string) error {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	payload := render.LimitResetPopupPayload(tool, limitAlarmPopupSec)
	payload["name"] = notifyNameUsageAlarm
	payload["soundRtttl"] = limitAlarmRTTTL
	if err := c.publisher.Notify(ctx, payload); err != nil {
		c.logger.Warn("limit alarm notify failed", "tool", tool, "err", err)
		return err
	}
	c.logger.Info("limit alarm fired", "tool", tool)
	return nil
}
