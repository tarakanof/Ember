package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

const hiddenAppsKey = "display_hidden_apps"

var baselineApps = []string{"claude", "codex", "t3"}

var errEmptyAppName = errors.New("app name is required")

type appDTO struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

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

func (a *App) setAppHidden(name string, hidden bool) {
	a.cfgMu.Lock()
	a.appsMu.Lock()
	if a.hiddenApps == nil {
		a.hiddenApps = map[string]bool{}
	}
	if hidden {
		a.hiddenApps[name] = true
	} else {
		delete(a.hiddenApps, name)
	}
	a.persistHiddenAppsLocked()
	a.appsMu.Unlock()
	a.cfgMu.Unlock()
	a.syncClockConfigVersion()
}

func (a *App) hiddenAppNames() []string {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	return a.hiddenAppNamesLocked()
}

func (a *App) hiddenAppNamesLocked() []string {
	names := make([]string, 0, len(a.hiddenApps))
	for n, v := range a.hiddenApps {
		if v {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func (a *App) setHiddenAppsMemory(names []string) {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	a.hiddenApps = make(map[string]bool, len(names))
	for _, n := range names {
		a.hiddenApps[n] = true
	}
}

func (a *App) persistHiddenAppsLocked() {
	if a.store == nil {
		return
	}
	blob, err := json.Marshal(a.hiddenAppNamesLocked())
	if err != nil {
		return
	}
	if err := a.store.PutSetting(hiddenAppsKey, string(blob)); err != nil {
		a.logger.Warn("hidden apps persist failed", "err", err)
	}
}

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
	if !a.decodeOrReject(w, r, &req, false) {
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
