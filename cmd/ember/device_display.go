package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// overlayValues are the ambient-weather overlay effects awtrix-ng exposes via
// PATCH /api/v1/display. There is no "clear" value in NG — clearing an
// overlay is overlay:null.
var overlayValues = enumOf("drizzle", "frost", "rain", "snow", "storm", "thunder")

// overlaySettingsRules validates the nested "overlaySettings" object.
var overlaySettingsRules = map[string]settingRule{
	"speed":   {kind: kNumber},
	"palette": {kind: kStringOrNull, maxLen: 32},
	"blend":   {kind: kBool},
}

// validateDeviceDisplay rejects unknown keys and out-of-range / wrong-type
// values for PUT /v1/device/display. Only overlay and overlaySettings are in
// scope for this ticket — moodlight and power are deliberately not exposed.
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
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/display", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (a *App) handleDeviceDisplayPut(w http.ResponseWriter, r *http.Request) {
	var m map[string]any
	if err := decodeJSON(w, r, &m, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateDeviceDisplay(m); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload, _ := json.Marshal(m)
	_, status, err := a.proxyToDevice(r.Context(), http.MethodPatch, "/api/v1/display", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// deviceAppsPutBody is the only shape PUT /v1/device/apps accepts — it
// replaces the old TIM/DAT/TEMP/HUM/BAT per-app toggles, which NG has no
// equivalent for. order/disabled are app-name strings; per NG's own contract,
// name only what you want to change.
type deviceAppsPutBody struct {
	Order    []string `json:"order"`
	Disabled []string `json:"disabled"`
}

func (a *App) handleDeviceAppsGet(w http.ResponseWriter, r *http.Request) {
	body, status, err := a.proxyToDevice(r.Context(), http.MethodGet, "/api/v1/apps", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (a *App) handleDeviceAppsPut(w http.ResponseWriter, r *http.Request) {
	var body deviceAppsPutBody
	if err := decodeJSON(w, r, &body, true); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	payload, _ := json.Marshal(body)
	_, status, err := a.proxyToDevice(r.Context(), http.MethodPut, "/api/v1/apps/order", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	w.WriteHeader(http.StatusOK)
}
