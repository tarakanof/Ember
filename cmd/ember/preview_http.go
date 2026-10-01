package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := render.DraftDisplay{
		ContextPct:     queryBool(q.Get("context_pct")),
		ActivityDetail: queryBool(q.Get("activity_detail")),
		RateBottomBar:  queryBool(q.Get("rate_bottom_bar")),
		SourceCard:     queryBoolDefault(q.Get("source_card"), true),
		SessionBar:     queryBoolDefault(q.Get("session_bar"), true),
		SourceColor:    strings.TrimSpace(q.Get("source_color")),
	}
	var u *render.UsageView
	if queryBoolDefault(q.Get("usage_card"), true) {
		u = render.SampleUsageView()
	}

	now := time.Now()
	snap := a.Snapshot()
	base := render.SampleBaseSession()
	if win, _, _ := render.PickWinning(snap.Sessions); win != nil {
		base = *win
	}
	s := render.PreviewSession(d, base)
	writeJSON(w, http.StatusOK, render.PreviewFrames(s, u, now))
}

func queryBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func queryBoolDefault(v string, def bool) bool {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return queryBool(v)
}
