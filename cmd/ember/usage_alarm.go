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

func (c *coordinator) checkLimitAlarms(now time.Time, snap Snapshot) {
	if c.usage == nil || !c.loadCfg().limitAlarmEnabled() {
		c.limitAlarmState.disable()
		return
	}
	for _, f := range c.limitAlarmState.due(c.usage.state(snap.Sessions, now), now) {
		if c.fireLimitAlarm(f.Tool) == nil {
			c.limitAlarmState.fired(f)
		}
	}
}

func (c *coordinator) fireLimitAlarm(tool string) error {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	n := notice{app: "usage", kind: noticeUsageReset, priority: noticeQueue,
		sound:   noticeSound{rtttl: limitAlarmRTTTL},
		payload: render.LimitResetPopupPayload(tool, limitAlarmPopupSec)}
	if err := c.showNotice(ctx, n); err != nil {
		c.logger.Warn("limit alarm notify failed", "tool", tool, "err", err)
		return err
	}
	c.logger.Info("limit alarm fired", "tool", tool)
	return nil
}
