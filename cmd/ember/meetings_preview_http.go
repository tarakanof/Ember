package main

import (
	"net/http"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/render"
)

// handleMeetingsPreview renders the ember-meet tile into the same 32×8 frame
// grid as /v1/preview, from the same tile view the coordinator pushes
// (previewTiles). Open and read-only. Uses the live next occurrence when the
// store is fresh, else a canned sample so the preview never renders blank.
// The device renders the text natively; the preview draws it in the 3×5 font.
func (a *App) handleMeetingsPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.meetingsPreview(time.Now()))
}

func (a *App) meetingsPreview(now time.Time) render.Preview {
	in := tileInputs{now: now, meet: a.cfg.Load().Meetings,
		nextMeet: meetings.Occurrence{Title: "STANDUP", Start: now.Add(12 * time.Minute)}}
	if occ, ok := a.meetings.next(now); ok && a.meetings.fresh(now) {
		in.nextMeet = occ
	}
	return previewTiles(in, meetTile.card)
}

// handleMeetingsState lists the next few upcoming occurrences for the menu
// app's sanity list. Open and read-only — titles are not secrets, the feed
// URLs never appear here. Timestamps are RFC3339 truncated to whole seconds:
// Go marshals fractional seconds by default and Swift's .iso8601 decoder
// rejects them.
// Intentionally does NOT gate on fresh() (unlike handleMeetingsPreview above):
// a stale list is more useful than an empty one; the caller receives fetched_at
// and can judge staleness itself.
func (a *App) handleMeetingsState(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	type item struct {
		UID   string    `json:"uid"`
		Title string    `json:"title"`
		Start time.Time `json:"start"`
	}
	occs := a.meetings.snapshot(now, 5)
	items := make([]item, 0, len(occs))
	for _, o := range occs {
		items = append(items, item{UID: o.UID, Title: o.Title, Start: o.Start.UTC().Truncate(time.Second)})
	}
	resp := struct {
		Upcoming  []item     `json:"upcoming"`
		FetchedAt *time.Time `json:"fetched_at,omitempty"`
	}{Upcoming: items}
	if ok := a.meetings.lastOK(); !ok.IsZero() {
		t := ok.UTC().Truncate(time.Second)
		resp.FetchedAt = &t
	}
	writeJSON(w, http.StatusOK, resp)
}
