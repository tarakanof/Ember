package main

import (
	"errors"
	"net/http"
	"regexp"
	"time"
)

// toolNameRe bounds the "tool" key an authed client can post: lowercase
// alnum/underscore/hyphen, 1..32 chars. Without this an authed-but-buggy
// client could grow UsageStore.byTool with unbounded garbage keys.
var toolNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// handleUsage stores a posted per-tool usage snapshot. The "POST /v1/usage"
// pattern enforces the verb, so no method check is needed here. Auth is applied
// by the requireAuth wrapper around the /v1/ mux.
func (a *App) handleUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool     string                  `json:"tool"`
		Source   string                  `json:"source"`
		FiveHour *UsageWindow            `json:"five_hour"`
		SevenDay *UsageWindow            `json:"seven_day"`
		Models   map[string]*UsageWindow `json:"models"`
	}
	if err := decodeJSON(w, r, &req, true); err != nil {
		var maxBytes *http.MaxBytesError
		reason := "parse"
		status := http.StatusBadRequest
		if errors.As(err, &maxBytes) {
			reason = "too_large"
			status = http.StatusRequestEntityTooLarge
		}
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", reason,
		)
		writeError(w, status, err)
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
	windows := []*UsageWindow{req.FiveHour, req.SevenDay}
	for _, win := range req.Models {
		windows = append(windows, win)
	}
	for _, win := range windows {
		if win == nil {
			continue
		}
		if win.UsedPercent < 0 || win.UsedPercent > 100 {
			a.logger.InfoContext(r.Context(), "request rejected",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"reason", "validation",
				"field", "used_percent",
			)
			writeError(w, http.StatusBadRequest, errors.New("used_percent must be within [0,100]"))
			return
		}
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
