package main

import (
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func meetingMinutes(now, start time.Time) int {
	m := int((start.Sub(now) + time.Minute - 1) / time.Minute)
	if m < 1 {
		m = 1
	}
	return m
}

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
		return tileView{
			payload: render.MeetingPayload(title, mins, usageAppLifetime),
			frame:   func() render.Frame { return render.MeetingTileFrame(title, mins) },
		}, true
	},
}
