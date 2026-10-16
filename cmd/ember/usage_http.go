package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	usageModelsMax        = 4
	usageResetLabelMax    = 32
	usageWindowMinutesMax = 31 * 24 * 60
	usageResetsAtPast     = 24 * time.Hour
	usageResetsAtAhead    = 30 * 24 * time.Hour
)

var (
	toolNameRe   = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	usageModelRe = regexp.MustCompile(`^[a-z0-9_-]{1,24}$`)
)

func usageModelsOK(models map[string]*UsageWindow) bool {
	if len(models) > usageModelsMax {
		return false
	}
	for name, w := range models {
		if !usageModelRe.MatchString(name) || !usageLabelOK(w) {
			return false
		}
	}
	return true
}

func usageLabelOK(w *UsageWindow) bool {
	return w == nil || len(w.ResetLabel) <= usageResetLabelMax
}

func clampUsageReport(u *ToolUsage, now time.Time) {
	clampUsageWindow(u.FiveHour, now)
	clampUsageWindow(u.SevenDay, now)
	for _, win := range u.Models {
		clampUsageWindow(win, now)
	}
}

func clampUsageWindow(win *UsageWindow, now time.Time) {
	if win == nil {
		return
	}
	if win.ResetsAt != 0 {
		win.ResetsAt = min(max(win.ResetsAt, now.Add(-usageResetsAtPast).Unix()), now.Add(usageResetsAtAhead).Unix())
	}
	if win.WindowMinutes < 0 || win.WindowMinutes > usageWindowMinutesMax {
		win.WindowMinutes = 0
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
		{"models", "", usageModelsOK(req.Models), "models: at most 4, each name matching ^[a-z0-9_-]{1,24}$"},
		{"reset_label", "", usageLabelOK(req.FiveHour) && usageLabelOK(req.SevenDay), "reset_label must be at most 32 bytes"},
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
	limit, principal := 0, ""
	if c, isClient := r.Context().Value(clientCallerKey{}).(clientCaller); isClient {
		limit, principal = usageAccountsTotal, "client:"+c.id
	}
	now := time.Now()
	rep := ToolUsage{
		FiveHour:  req.FiveHour,
		SevenDay:  req.SevenDay,
		Models:    req.Models,
		Source:    req.Source,
		Account:   req.AccountKey,
		Plan:      req.Plan,
		Principal: principal,
	}
	clampUsageReport(&rep, now)
	k := usageKey{req.Tool, req.AccountKey}
	owner, masterHeld, refused := a.usageSeen.refuses(k, principal, a.usageWriterAlive, now)
	rep.MasterHeld = masterHeld
	var err error
	if !refused {
		owner, err = a.usage.PutIfOwned(req.Tool, rep, a.usageWriterAlive, limit, now)
	}
	switch {
	case refused, errors.Is(err, errUsageNotOwned):
		a.denyUsageOwner(w, r, req.Tool, owner)
		return
	case errors.Is(err, errUsageFull):
		a.logger.WarnContext(r.Context(), "usage post rejected", "tool", req.Tool, "reason", "account_limit")
		writeError(w, http.StatusInsufficientStorage, err)
		return
	}
	if err := a.usageSeen.observe(k, rep, owner, now); err != nil {
		a.logger.Warn("usage account persist failed", "tool", req.Tool, "err", err)
	}
	a.pruneUsageAccountPrefs()
	a.changes.notify(topicUsage)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) usageWriterAlive(principal string) bool {
	id, ok := strings.CutPrefix(principal, "client:")
	return !ok || a.clients == nil || a.clients.exists(id)
}

func (a *App) denyUsageOwner(w http.ResponseWriter, r *http.Request, tool, owner string) {
	kind, label := "client", "another client token"
	if owner == "" {
		kind, label = "master", "the master token"
	}
	c, _ := r.Context().Value(clientCallerKey{}).(clientCaller)
	a.logger.InfoContext(r.Context(), "usage post rejected",
		"client_id", c.id, "tool", tool, "reason", "owned", "owner", kind)
	writeError(w, http.StatusForbidden, fmt.Errorf("%w: %s posted it in the last 10 min", errUsageNotOwned, label))
}
