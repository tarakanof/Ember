package main

import "time"

// attentionRTTTL is the optional lock-acquisition chime (display.attention_chime).
const attentionRTTTL = "attn:d=16,o=6,b=200:c,e,g"

// ackTimeoutDur reads the attention-hold duration live so a /v1/display/config
// PUT applies to the CURRENT lock without restart.
func (c *coordinator) ackTimeoutDur() time.Duration {
	sec := c.loadCfg().Display.AckTimeoutSeconds
	if sec <= 0 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

// armLockTimerLocked installs (or replaces) the wallclock safety-net
// timer that fires a cmdTick after the current ackTimeoutDur. Caller must hold muTest.
// The timer is armed with the value at arm time; if the hold is shortened
// mid-lock via a config PUT, the tick-driven release check in onTick still
// releases promptly (it re-reads live on every tick), but this safety-net
// timer may fire later than the new shorter value.
func (c *coordinator) armLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
	}
	c.lockReleaseTimer = time.AfterFunc(c.ackTimeoutDur(), func() {
		c.Send(coordCmd{kind: cmdTick})
	})
}

// disarmLockTimerLocked stops the safety-net timer. Caller must hold muTest.
func (c *coordinator) disarmLockTimerLocked() {
	if c.lockReleaseTimer != nil {
		c.lockReleaseTimer.Stop()
		c.lockReleaseTimer = nil
	}
}
