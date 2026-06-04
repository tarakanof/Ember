package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

// hiddenAppsKey is the store key holding the JSON array of tool names hidden
// from the device display.
const hiddenAppsKey = "display_hidden_apps"

// baselineApps always appear in the menu toggle list even when idle, so the user
// can pre-hide a tool before it first reports.
var baselineApps = []string{"claude", "codex"}

var errEmptyAppName = errors.New("app name is required")

type appDTO struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// hiddenAppsSet returns a copy of the hidden-tool set.
func (a *App) hiddenAppsSet() map[string]bool {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	out := make(map[string]bool, len(a.hiddenApps))
	for k, v := range a.hiddenApps {
		if v {
			out[k] = true
		}
	}
	return out
}

// loadHiddenApps reads the persisted hidden set from the store (no-op if the
// store is absent or the key is unset).
func (a *App) loadHiddenApps() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(hiddenAppsKey)
	if err != nil || !ok {
		return
	}
	var names []string
	if err := json.Unmarshal([]byte(blob), &names); err != nil {
		a.logger.Warn("hidden apps parse failed", "err", err)
		return
	}
	a.appsMu.Lock()
	a.hiddenApps = map[string]bool{}
	for _, n := range names {
		a.hiddenApps[n] = true
	}
	a.appsMu.Unlock()
}

// setAppHidden updates the in-memory hidden set and persists it. Persistence
// failure is logged but non-fatal (matches the Pomodoro-settings behaviour).
func (a *App) setAppHidden(name string, hidden bool) {
	a.appsMu.Lock()
	if a.hiddenApps == nil {
		a.hiddenApps = map[string]bool{}
	}
	if hidden {
		a.hiddenApps[name] = true
	} else {
		delete(a.hiddenApps, name)
	}
	names := make([]string, 0, len(a.hiddenApps))
	for n := range a.hiddenApps {
		names = append(names, n)
	}
	a.appsMu.Unlock()
	sort.Strings(names)
	if a.store != nil {
		if blob, err := json.Marshal(names); err == nil {
			if err := a.store.PutSetting(hiddenAppsKey, string(blob)); err != nil {
				a.logger.Warn("hidden apps persist failed", "err", err)
			}
		}
	}
}

// knownApps is the union of baseline tools, tools seen in the live snapshot, and
// any currently-hidden tool, each with its enabled flag.
func (a *App) knownApps() []appDTO {
	hidden := a.hiddenAppsSet()
	set := map[string]bool{}
	for _, n := range baselineApps {
		set[n] = true
	}
	for _, s := range a.Snapshot().Sessions {
		if s.Tool != "" {
			set[s.Tool] = true
		}
	}
	for n := range hidden {
		set[n] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]appDTO, 0, len(names))
	for _, n := range names {
		out = append(out, appDTO{Name: n, Enabled: !hidden[n]})
	}
	return out
}

func (a *App) handleAppsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"apps": a.knownApps()})
}

func (a *App) handleAppsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		App     string `json:"app"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.App == "" {
		writeError(w, http.StatusBadRequest, errEmptyAppName)
		return
	}
	a.setAppHidden(req.App, !req.Enabled)
	a.nudgePomo()
	writeJSON(w, http.StatusOK, map[string]any{"apps": a.knownApps()})
}
