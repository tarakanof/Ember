package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// The clock's temperature/humidity calibration lives in tempOffset/humOffset
// on /api/v1/system, alongside Wi-Fi credentials and everything else the
// device needs to boot. Even though the docs describe PUT /api/v1/system as
// accepting a partial object, a full-replace read-merge-PUT is used here
// regardless: it is correct under both partial- and full-replace semantics,
// and a bug in that assumption with a naive partial PUT could silently drop
// the stored Wi-Fi password and brick the clock's connectivity. NG applies
// system changes live — no reboot follows a successful write.

// sensorOffsetKeys maps Ember's stable snake_case wire keys to the NG
// system-object camelCase keys, with the clamp each is bounded to. Offsets
// are degrees (temp) / percentage points (humidity).
var sensorOffsetKeys = map[string]struct {
	sysKey string
	limit  float64
}{
	"temp_offset": {"tempOffset", 20},
	"hum_offset":  {"humOffset", 50},
}

// defaultTempOffset/defaultHumOffset are the Ulanzi TC001's firmware-default
// calibration values (confirmed against reference/system and the live
// device). An explicit null in a PUT /v1/device/sensors patch resets the
// corresponding offset to these — the closest equivalent, on a system object
// that has no "unset" state, to AWTRIX3's dev.json "key absent" default.
const (
	defaultTempOffset = -9.0
	defaultHumOffset  = 0.0
)

// readSystem fetches the clock's current /api/v1/system object in full.
func (a *App) readSystem(ctx context.Context) (map[string]any, error) {
	body, status, err := a.proxyToDevice(ctx, http.MethodGet, "/api/v1/system", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("clock returned %d for /api/v1/system", status)
	}
	m := map[string]any{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("clock /api/v1/system: %w", err)
	}
	return m, nil
}

// handleDeviceSensorsGet reports the calibration offsets currently on the
// clock's /api/v1/system. Unlike the old dev.json contract, the system object
// always has a value for both keys — there is no "not set" state to report as
// null; the values reported here are simply whatever the device holds now.
func (a *App) handleDeviceSensorsGet(w http.ResponseWriter, r *http.Request) {
	sys, err := a.readSystem(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := struct {
		TempOffset *float64 `json:"temp_offset"`
		HumOffset  *float64 `json:"hum_offset"`
	}{}
	if f, ok := sys["tempOffset"].(float64); ok {
		out.TempOffset = &f
	}
	if f, ok := sys["humOffset"].(float64); ok {
		out.HumOffset = &f
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeviceSensorsPut merges offset changes into the clock's full
// /api/v1/system object and writes it back. A number sets the offset, an
// explicit null resets it to the firmware default, an absent key is left
// untouched. Applies live; no reboot. Keeps Ember's own snake_case
// temp_offset/hum_offset wire contract so callers written against the old
// dev.json-backed endpoint keep working.
func (a *App) handleDeviceSensorsPut(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decodeJSON(w, r, &patch, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(patch) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no offsets given"))
		return
	}
	for k, v := range patch {
		key, ok := sensorOffsetKeys[k]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unknown key %q", k))
			return
		}
		if v == nil {
			continue
		}
		f, ok := v.(float64)
		if !ok || f < -key.limit || f > key.limit {
			writeError(w, http.StatusBadRequest,
				fmt.Errorf("%s must be null or a number in [%.0f,%.0f]", k, -key.limit, key.limit))
			return
		}
	}

	sys, err := a.readSystem(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for k, v := range patch {
		key := sensorOffsetKeys[k]
		if v == nil {
			if key.sysKey == "tempOffset" {
				sys[key.sysKey] = defaultTempOffset
			} else {
				sys[key.sysKey] = defaultHumOffset
			}
			continue
		}
		sys[key.sysKey] = v
	}
	payload, err := json.Marshal(sys)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_, status, err := a.proxyToDevice(r.Context(), http.MethodPut, "/api/v1/system", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Errorf("clock returned %d", status))
		return
	}
	a.handleDeviceSensorsGet(w, r)
}
