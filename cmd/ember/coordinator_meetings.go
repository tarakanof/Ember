package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// meetingMinutes is the displayed whole-minute countdown: ceil(remaining), min 1.
// The tile never shows "0m" — it leaves the rotation at start.
func meetingMinutes(now, start time.Time) int {
	m := int((start.Sub(now) + time.Minute - 1) / time.Minute)
	if m < 1 {
		m = 1
	}
	return m
}

// reconcileMeetingApp pushes/refreshes the standalone "ember-meet" countdown
// tile when meetings are enabled, the next meeting is inside the lead window,
// and the feed data is fresh; clears it otherwise (including at meeting start —
// the countdown never shows 0m). The minute-by-minute countdown needs no timer:
// the payload text changes each minute, so the bytes-diff naturally re-pushes.
// Coordinator goroutine only.
func (c *coordinator) reconcileMeetingApp(now time.Time) {
	if c.meetings == nil {
		return
	}
	cfg := c.loadCfg().Meetings
	occ, ok := c.meetings.next(now)
	want := cfg.IsEnabled() && ok && c.meetings.fresh(now) &&
		occ.Start.Sub(now) <= time.Duration(cfg.TileLeadMinutes)*time.Minute
	c.reconcileTile(now, "ember-meet", &c.pushedMeeting, want, func() map[string]any {
		return render.MeetingPayload(sanitizeMeetingTitle(occ.Title), meetingMinutes(now, occ.Start), usageAppLifetime)
	})
}
