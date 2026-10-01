package main

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"time"
)

var toolNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

func clampUsageWindow(win *UsageWindow) {
	if win == nil {
		return
	}
	switch {
	case math.IsNaN(win.UsedPercent), win.UsedPercent < 0:
		win.UsedPercent = 0
	case win.UsedPercent > 100:
		win.UsedPercent = 100
	}
}

func (a *App) handleUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool     string                  `json:"tool"`
		Source   string                  `json:"source"`
		FiveHour *UsageWindow            `json:"five_hour"`
		SevenDay *UsageWindow            `json:"seven_day"`
		Models   map[string]*UsageWindow `json:"models"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if !toolNameRe.MatchString(req.Tool) {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", "tool",
		)
		writeError(w, http.StatusBadRequest, errors.New("tool must match ^[a-z0-9_-]{1,32}$"))
		return
	}
	clampUsageWindow(req.FiveHour)
	clampUsageWindow(req.SevenDay)
	for _, win := range req.Models {
		clampUsageWindow(win)
	}
	a.usage.Put(req.Tool, ToolUsage{
		FiveHour:  req.FiveHour,
		SevenDay:  req.SevenDay,
		Models:    req.Models,
		Source:    req.Source,
		UpdatedAt: time.Now(),
	})
	w.WriteHeader(http.StatusNoContent)
}
