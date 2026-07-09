package main

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"time"
)

// toolNameRe bounds the "tool" key an authed client can post: lowercase
// alnum/underscore/hyphen, 1..32 chars. Without this an authed-but-buggy
// client could grow UsageStore.byTool with unbounded garbage keys.
var toolNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// clampUsageWindow normalizes a posted window's UsedPercent into [0,100]
// in place (negative → 0, >100 → 100). NaN — unreachable via JSON, which
// has no NaN literal, but conceivable from a future non-JSON caller — is
// normalized to 0 rather than stored (NaN poisons every comparison
// downstream, e.g. the usage-threshold gate). nil windows are a no-op.
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

// handleUsage stores a posted per-tool usage snapshot. The "POST /v1/usage"
// pattern enforces the verb, so no method check is needed here. Auth is applied
// by the requireAuth wrapper around the /v1/ mux.
//
// decodeJSON is called with strict=true, so an unknown field 400s here —
// unlike the non-strict POST /v1/status. Deliberate tradeoff: this matches
// handleNotify's discipline (also strict) rather than status's leniency,
// since both producers are updated in lockstep with this server and a typo'd
// field should surface immediately instead of silently being dropped.
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
	// The producers post UNCLAMPED upstream values (the claude producer
	// forwards Anthropic's raw utilization; the codex producer keeps raw
	// floats), and out-of-range values genuinely occur — so clamp into
	// [0,100] instead of rejecting, matching the coordinator's own pctInt
	// convention.
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
