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
