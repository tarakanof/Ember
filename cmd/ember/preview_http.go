package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// handlePreview renders the current winning session (or a sample) under the
// draft display toggles into per-card 32x8 color grids. Open and read-only:
// it never publishes to the device or stores state.
func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := render.DraftDisplay{
		ContextPct:     queryBool(q.Get("context_pct")),
		RatePct:        queryBool(q.Get("rate_pct")),
		ActivityDetail: queryBool(q.Get("activity_detail")),
		ContextNumber:  queryBool(q.Get("context_number")),
		RateBottomBar:  queryBool(q.Get("rate_bottom_bar")),
		RateReset:      queryBool(q.Get("rate_reset")),
		SourceCard:     queryBoolDefault(q.Get("source_card"), true),
		SessionBar:     queryBoolDefault(q.Get("session_bar"), true),
		SourceColor:    strings.TrimSpace(q.Get("source_color")),
	}

	now := time.Now()
	snap := a.Snapshot()
	base := render.SampleBaseSession()
	if win, _, _ := render.PickWinning(snap.Sessions); win != nil {
		base = *win
	}
	s := render.PreviewSession(d, base, now)
	writeJSON(w, http.StatusOK, render.PreviewFrames(s, now))
}

// queryBool treats explicit truthy strings as true; anything else (including
// absent) is false. The client always sends explicit values resolved from the
// producer.env defaults, so absence does not occur in normal use.
func queryBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// queryBoolDefault returns def when v is absent/blank, otherwise delegates to
// queryBool. The new params (source_card, session_bar) default ON so a
// pre-rework menu app (which doesn't send them) previews unchanged.
func queryBoolDefault(v string, def bool) bool {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return queryBool(v)
}
