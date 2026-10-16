package main

import (
	"cmp"
	"regexp"
	"slices"
	"time"
)

const (
	knobUsageMaxAccounts  = 8
	knobUsageMaxModels    = 2
	knobUsageFiveHourSecs = 5 * 60 * 60
)

var knobUsageModelRe = regexp.MustCompile(`^[a-z0-9_-]{1,16}$`)

type knobUsage struct {
	Accounts []knobUsageAccount `json:"accounts"`
}

type knobUsageAccount struct {
	ID        string           `json:"id"`
	Tool      string           `json:"tool"`
	Name      string           `json:"name"`
	FiveHour  *knobUsageWindow `json:"five_hour,omitempty"`
	SevenDay  *knobUsageWindow `json:"seven_day,omitempty"`
	Models    []knobUsageModel `json:"models,omitempty"`
	Stale     bool             `json:"stale"`
	UpdatedAt int64            `json:"updated_at,omitempty"`
}

type knobUsageWindow struct {
	Pct      int   `json:"pct"`
	ResetsAt int64 `json:"resets_at,omitempty"`
	StartsAt int64 `json:"starts_at,omitempty"`
}

type knobUsageModel struct {
	Name string `json:"name"`
	Pct  int    `json:"pct"`
}

var knobUsageModelOrder = map[string]int{"opus": 0, "sonnet": 1}

func knobWindow(w *UsageWindow, length int64, now time.Time) *knobUsageWindow {
	if w == nil {
		return nil
	}
	if w.ResetsAt > 0 && w.ResetsAt <= now.Unix() {
		return &knobUsageWindow{}
	}
	out := &knobUsageWindow{Pct: pctInt(w.UsedPercent), ResetsAt: w.ResetsAt}
	if length > 0 && w.ResetsAt > 0 {
		out.StartsAt = w.ResetsAt - length
	}
	return out
}

func fiveHourSecs(w *UsageWindow) int64 {
	if w != nil && w.WindowMinutes > 0 {
		return w.WindowMinutes * 60
	}
	return knobUsageFiveHourSecs
}

func knobModels(models map[string]*UsageWindow, now time.Time) []knobUsageModel {
	names := make([]string, 0, len(models))
	for name, w := range models {
		if w != nil && knobUsageModelRe.MatchString(name) {
			names = append(names, name)
		}
	}
	rank := func(n string) int {
		if r, ok := knobUsageModelOrder[n]; ok {
			return r
		}
		return len(knobUsageModelOrder)
	}
	slices.SortFunc(names, func(x, y string) int { return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(x, y)) })
	var out []knobUsageModel
	for _, name := range names[:min(len(names), knobUsageMaxModels)] {
		out = append(out, knobUsageModel{Name: capitalize(name), Pct: knobWindow(models[name], 0, now).Pct})
	}
	return out
}

func (a *App) knobUsage(now time.Time) *knobUsage {
	perModel := a.cfg.Load().usagePerModelEnabled()
	var out knobUsage
	for _, acc := range a.usageAccounts(now) {
		if acc.Hidden || acc.Report == nil {
			continue
		}
		u := acc.Report
		k := knobUsageAccount{
			ID:       acc.ID,
			Tool:     acc.Tool,
			Name:     acc.Name,
			FiveHour: knobWindow(u.FiveHour, fiveHourSecs(u.FiveHour), now),
			SevenDay: knobWindow(u.SevenDay, 0, now),
			Stale:    !acc.Fresh,
		}
		if perModel {
			k.Models = knobModels(u.Models, now)
		}
		if k.Stale {
			k.UpdatedAt = u.UpdatedAt.Unix()
		}
		out.Accounts = append(out.Accounts, k)
		if len(out.Accounts) == knobUsageMaxAccounts {
			break
		}
	}
	if len(out.Accounts) == 0 {
		return nil
	}
	return &out
}
