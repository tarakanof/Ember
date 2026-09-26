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

// meetTile is "ember-meet": the countdown to the next meeting, in the rotation
// while it is inside tile_lead_minutes and the feed is fresh. It leaves at
// meeting start (the countdown never shows 0m). The countdown needs no timer:
// the text changes each minute, so the ledger's bytes diff re-pushes it.
var meetTile = tile{
	app:    "ember-meet",
	card:   "meeting",
	toggle: func(*tileInputs) bool { return true },
	live: func(in *tileInputs) bool {
		return in.meet.IsEnabled() && in.haveMeet && in.meetFresh &&
			in.nextMeet.Start.Sub(in.now) <= time.Duration(in.meet.TileLeadMinutes)*time.Minute
	},
	view: func(in *tileInputs) (tileView, bool) {
		title := sanitizeMeetingTitle(in.nextMeet.Title)
		mins := meetingMinutes(in.now, in.nextMeet.Start)
		// The device renders the text natively; the frame draws the same text
		// in the 3×5 font for the preview.
		return tileView{
			payload: render.MeetingPayload(title, mins, usageAppLifetime),
			frame:   func() render.Frame { return render.MeetingTileFrame(title, mins) },
		}, true
	},
}
