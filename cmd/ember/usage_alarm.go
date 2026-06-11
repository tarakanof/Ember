package main

import (
	"context"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const (
	// limitAlarmThreshold treats a 5h window as maxed (endpoint percents are
	// rounded, so 99.5 catches an int 100 without firing at a real 99).
	limitAlarmThreshold = 99.5
	limitAlarmGraceSec  = 60 // reset estimates drift; fire a minute late, never early
	limitAlarmPopupSec  = 10
	limitAlarmRTTTL     = "reset:d=8,o=6,b=160:g,8p,c7,8p,e7"
)

// effectiveFiveHour returns the freshest 5h window for tool: endpoint usage
// when fresh, else the newest live session's statusline data — the same
// precedence the usage-app fallback uses.
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

// checkLimitAlarms arms when a tool's 5h window is maxed with a known reset
// time, and fires one popup+chime once that reset (plus grace) passes.
// Armed/fired state is in-memory: a restart mid-window simply re-arms from the
// next snapshot. Coordinator-goroutine-owned.
func (c *coordinator) checkLimitAlarms(now time.Time, snap Snapshot) {
	if c.usage == nil || !c.loadCfg().limitAlarmEnabled() {
		// Drop any armed state so re-enabling hours later can't fire a stale
		// "reset" popup for a window that long since passed.
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
		// Due. If fresh data says the window is still maxed with a LATER
		// reset, the estimate drifted — re-arm, don't fire.
		if ok && pct >= limitAlarmThreshold && resetAt > armed {
			c.alarmArmed[tool] = resetAt
			continue
		}
		if err := c.fireLimitAlarm(tool); err != nil {
			continue // device unreachable: retry next tick, stay armed
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
	if err := c.publisher.Notify(ctx, render.LimitResetPopupPayload(tool, limitAlarmPopupSec)); err != nil {
		c.logger.Warn("limit alarm notify failed", "tool", tool, "err", err)
		return err
	}
	if err := c.publisher.PlayRTTTL(ctx, limitAlarmRTTTL); err != nil {
		c.logger.Warn("limit alarm chime failed", "err", err) // popup shown; don't retry
	}
	c.logger.Info("limit alarm fired", "tool", tool)
	return nil
}
