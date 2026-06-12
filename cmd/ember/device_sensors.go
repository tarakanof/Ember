package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
)

// The clock's temperature/humidity calibration lives in /dev.json on its
// LittleFS (keys temp_offset, hum_offset), not in /api/settings, and is only
// read at boot — so a write here is read-merge-upload followed by a reboot.
// dev.json also carries unrelated keys (notably button_callback, which the
// Pomodoro buttons depend on); the merge must preserve them.

// sensorOffsetKeys are the dev.json keys PUT /v1/device/sensors may touch.
// Offsets are degrees / percentage points, so |50| is already absurdly large.
var sensorOffsetKeys = map[string]bool{"temp_offset": true, "hum_offset": true}

const sensorOffsetLimit = 50.0

// readDevJSON fetches the clock's current /dev.json. A clock that has never
// had one returns 404, which counts as an empty config, not an error.
func (a *App) readDevJSON(ctx context.Context) (map[string]any, error) {
	body, status, err := a.proxyToDevice(ctx, http.MethodGet, "/dev.json", nil)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return map[string]any{}, nil
	case status != http.StatusOK:
		return nil, fmt.Errorf("clock returned %d for /dev.json", status)
	}
	m := map[string]any{}
	if len(bytes.TrimSpace(body)) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("clock /dev.json: %w", err)
	}
	return m, nil
}

// putDeviceFile uploads a file to the clock's LittleFS via its web file
// manager (multipart POST /edit; the part's filename is the target path).
// Same mechanism as HTTPPublisher.PutIcon, but for arbitrary paths.
func (a *App) putDeviceFile(ctx context.Context, path string, data []byte) error {
	base, cl, err := a.deviceBaseClient()
	if err != nil {
		return err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("data", path)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/edit", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("device /edit upload %s: status %d", path, resp.StatusCode)
	}
	return nil
}

// handleDeviceSensorsGet reports the calibration offsets currently in the
// clock's dev.json. null means "not set" — the firmware's compiled-in default
// applies (-9 temperature / 0 humidity on the Ulanzi build).
func (a *App) handleDeviceSensorsGet(w http.ResponseWriter, r *http.Request) {
	dev, err := a.readDevJSON(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := struct {
		TempOffset *float64 `json:"temp_offset"`
		HumOffset  *float64 `json:"hum_offset"`
	}{}
	if f, ok := dev["temp_offset"].(float64); ok {
		out.TempOffset = &f
	}
	if f, ok := dev["hum_offset"].(float64); ok {
		out.HumOffset = &f
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeviceSensorsPut merges offset changes into the clock's dev.json and
// reboots it (offsets are only read at boot). A number sets the offset, an
// explicit null removes it (back to the firmware default), an absent key is
// left untouched.
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
		if !sensorOffsetKeys[k] {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unknown key %q", k))
			return
		}
		if v == nil {
			continue
		}
		f, ok := v.(float64)
		if !ok || f < -sensorOffsetLimit || f > sensorOffsetLimit {
			writeError(w, http.StatusBadRequest,
				fmt.Errorf("%s must be null or a number in [%.0f,%.0f]", k, -sensorOffsetLimit, sensorOffsetLimit))
			return
		}
	}

	dev, err := a.readDevJSON(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for k, v := range patch {
		if v == nil {
			delete(dev, k)
		} else {
			dev[k] = v
		}
	}
	payload, err := json.Marshal(dev)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := a.putDeviceFile(r.Context(), "/dev.json", payload); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if _, status, err := a.proxyToDevice(r.Context(), http.MethodPost, "/api/reboot", []byte("{}")); err != nil || status < 200 || status >= 300 {
		if err == nil {
			err = fmt.Errorf("clock returned %d", status)
		}
		writeError(w, http.StatusBadGateway, fmt.Errorf("dev.json written but reboot failed (offsets apply on next boot): %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rebooted": true})
}
