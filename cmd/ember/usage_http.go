package main

import (
	"encoding/json"
	"net/http"
	"time"
)

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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tool == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
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
