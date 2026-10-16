package main

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
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
		Tool       string                  `json:"tool"`
		Source     string                  `json:"source"`
		AccountKey string                  `json:"account_key"`
		Plan       string                  `json:"plan"`
		FiveHour   *UsageWindow            `json:"five_hour"`
		SevenDay   *UsageWindow            `json:"seven_day"`
		Models     map[string]*UsageWindow `json:"models"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	for _, f := range []struct {
		name, value string
		ok          bool
		msg         string
	}{
		{"tool", req.Tool, toolNameRe.MatchString(req.Tool), "tool must match ^[a-z0-9_-]{1,32}$"},
		{"account_key", req.AccountKey, req.AccountKey == "" || accountKeyRe.MatchString(req.AccountKey), "account_key must be 16 lower-case hex characters"},
		{"plan", req.Plan, req.Plan == "" || planRe.MatchString(req.Plan), "plan must match ^[a-z0-9_]{1,24}$"},
	} {
		if f.ok {
			continue
		}
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", f.name,
		)
		writeError(w, http.StatusBadRequest, errors.New(f.msg))
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	if !a.allowSource(w, r, req.Source) {
		return
	}
	clampUsageWindow(req.FiveHour)
	clampUsageWindow(req.SevenDay)
	for _, win := range req.Models {
		clampUsageWindow(win)
	}
	sources, _ := boundSources(r)
	limit := 0
	if _, isClient := r.Context().Value(clientCallerKey{}).(clientCaller); isClient {
		limit = usageAccountsTotal
	}
	now := time.Now()
	owner, err := a.usage.PutIfOwned(req.Tool, ToolUsage{
		FiveHour: req.FiveHour,
		SevenDay: req.SevenDay,
		Models:   req.Models,
		Source:   req.Source,
		Account:  req.AccountKey,
		Plan:     req.Plan,
	}, sources, limit, now)
	switch {
	case errors.Is(err, errUsageNotOwned):
		a.denySource(w, r, owner)
		return
	case errors.Is(err, errUsageFull):
		a.logger.WarnContext(r.Context(), "usage post rejected", "tool", req.Tool, "reason", "account_limit")
		writeError(w, http.StatusInsufficientStorage, err)
		return
	}
	if err := a.usageSeen.observe(usageKey{req.Tool, req.AccountKey}, req.Plan, req.Source, now); err != nil {
		a.logger.Warn("usage account persist failed", "tool", req.Tool, "err", err)
	}
	a.changes.notify(topicUsage)
	w.WriteHeader(http.StatusNoContent)
}
