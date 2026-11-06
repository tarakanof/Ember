package main

import "time"

const attentionRTTTL = "attn:d=16,o=6,b=200:c,e,g"

func (c *coordinator) ackTimeoutDur() time.Duration {
	sec := c.loadCfg().Display.AckTimeoutSeconds
	if sec <= 0 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

func (c *coordinator) armLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
	}
	c.lockReleaseTimer = time.AfterFunc(c.ackTimeoutDur(), func() {
		c.Send(coordCmd{kind: cmdTick})
	})
}

func (c *coordinator) disarmLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
		c.lockReleaseTimer = nil
	}
}

func (c *coordinator) applyAttentionLocked(step attentionStep, key string) {
	switch step {
	case attentionAcquire:
		c.pointer = key
		c.cardCursor = 0
		c.attentionState.acquire(key, c.clk.Now())
		c.armLockTimerLocked()
	case attentionRenew:
		c.attentionState.renew(c.clk.Now())
		c.armLockTimerLocked()
	case attentionDrain:
		c.releaseLockLocked(attentionEndDrain)
	}
}

func (c *coordinator) releaseLockLocked(end attentionEnd) {
	c.logger.Info("coord lock released", "key", c.lockedKey, "reason", string(end))
	c.attentionState.release()
	c.disarmLockTimerLocked()
}
