package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tarakanof/ember/internal/awtrix"
)

var overlayValues = enumOf("drizzle", "frost", "rain", "snow", "storm", "thunder")

var overlaySettingsRules = map[string]settingRule{
	"speed":   {kind: kNumber},
	"palette": {kind: kStringOrNull, maxLen: 32},
	"blend":   {kind: kBool},
}

func validateDeviceDisplay(m map[string]any) error {
	for k, v := range m {
		switch k {
		case "overlay":
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok || !overlayValues[s] {
				return fmt.Errorf("overlay must be null or one of drizzle|frost|rain|snow|storm|thunder")
			}
		case "overlaySettings":
			obj, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("overlaySettings must be an object")
			}
			if err := validateAgainstRules(obj, overlaySettingsRules); err != nil {
				return fmt.Errorf("overlaySettings.%w", err)
			}
		default:
			return fmt.Errorf("unknown setting %q", k)
		}
	}
	return nil
}

func (a *App) handleDeviceDisplayGet(w http.ResponseWriter, r *http.Request) {
	a.proxyRead(w, r, (*awtrix.Client).RawDisplay)
}

func (a *App) handleDeviceDisplayPut(w http.ResponseWriter, r *http.Request) {
	var m map[string]any
	if !a.decodeOrReject(w, r, &m, false) {
		return
	}
	if err := validateDeviceDisplay(m); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload, _ := json.Marshal(m)
	a.proxyAction(w, r, withBody((*awtrix.Client).RawPatchDisplay, payload))
}

type deviceAppsPutBody struct {
	Order    []string `json:"order"`
	Disabled []string `json:"disabled"`
}

func (a *App) handleDeviceAppsGet(w http.ResponseWriter, r *http.Request) {
	body, err := a.clock.fetch(r.Context(), (*awtrix.Client).RawApps)
	if err != nil {
		writeClockError(w, err)
		return
	}
	a.noteClockApps(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) handleDeviceAppsPut(w http.ResponseWriter, r *http.Request) {
	var body deviceAppsPutBody
	if !a.decodeOrReject(w, r, &body, true) {
		return
	}
	payload, _ := json.Marshal(body)
	if _, err := a.clock.fetch(r.Context(), withBody((*awtrix.Client).RawPutAppOrder, payload)); err != nil {
		writeClockError(w, err)
		return
	}
	a.clockRotation.Store(nil)
	w.WriteHeader(http.StatusOK)
}
