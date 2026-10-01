package main

import (
	"fmt"
	"net/http"
)

var sensorOffsetKeys = map[string]struct {
	sysKey string
	limit  float64
}{
	"temp_offset": {"tempOffset", 20},
	"hum_offset":  {"humOffset", 50},
}

const (
	defaultTempOffset = -9.0
	defaultHumOffset  = 0.0
)

func (a *App) handleDeviceSensorsGet(w http.ResponseWriter, r *http.Request) {
	sys, err := a.clock.readSystem(r.Context())
	if err != nil {
		writeClockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sensorOffsets(sys))
}

type sensorOffsetsResponse struct {
	TempOffset *float64 `json:"temp_offset"`
	HumOffset  *float64 `json:"hum_offset"`
}

func sensorOffsets(sys map[string]any) sensorOffsetsResponse {
	var out sensorOffsetsResponse
	if f, ok := sys["tempOffset"].(float64); ok {
		out.TempOffset = &f
	}
	if f, ok := sys["humOffset"].(float64); ok {
		out.HumOffset = &f
	}
	return out
}

func (a *App) handleDeviceSensorsPut(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if !a.decodeOrReject(w, r, &patch, false) {
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

	ctx, cancel := a.clock.writeContext(r.Context())
	defer cancel()
	written, err := a.clock.updateSystem(ctx, func(sys map[string]any) {
		for k, v := range patch {
			key := sensorOffsetKeys[k]
			switch {
			case v != nil:
				sys[key.sysKey] = v
			case key.sysKey == "tempOffset":
				sys[key.sysKey] = defaultTempOffset
			default:
				sys[key.sysKey] = defaultHumOffset
			}
		}
	})
	if err != nil {
		a.clock.writeBudgetError(ctx, w, err, false)
		return
	}
	sys, err := a.clock.readSystem(ctx)
	if err != nil {
		if ctx.Err() == nil {
			writeClockError(w, err)
			return
		}
		sys = written
	}
	writeJSON(w, http.StatusOK, sensorOffsets(sys))
}
