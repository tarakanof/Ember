package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	firmwareDownloadBurst  = 6
	firmwareDownloadPerSec = 0.1
)

func (a *App) pomodoroBusy() bool {
	if !a.pomodoroOn() {
		return false
	}
	st := a.engine.Status(time.Now())
	return st.Running || st.Paused
}

func (a *App) otaCheckin(id string, report deviceCheckin, res *checkinResult) error {
	if a.knobFW == nil {
		return nil
	}
	snap, _, err := a.devices.otaSnapshot(id)
	if err != nil {
		return err
	}
	in := otaInput{
		report:   report,
		now:      a.devices.now().UTC(),
		pomodoro: a.pomodoroBusy(),
		coredump: res.CoredumpWanted != "",
	}
	if snap.Target != "" {
		if m, ok := a.knobFW.get(snap.Target); ok {
			in.target = &m
		}
	} else if snap.mode() == otaModeAuto {
		m, ok := a.knobFW.newestAbove(report.FW, func(m firmwareMeta) bool {
			return m.Channel == firmwareChannelRelease && !slices.Contains(snap.Blocked, m.Version)
		})
		if ok {
			in.auto = &m
		}
	}
	var offer *otaOffer
	var waiting string
	_, _, err = a.devices.updateOTA(id, func(o *knobOTA, _ *deviceCheckin) (bool, error) {
		offer, waiting = nil, ""
		if o.Target != snap.Target || o.mode() != snap.mode() {
			applyOTAResult(o, report, in.now)
			return false, nil
		}
		offer, waiting = stepOTA(o, in)
		return false, nil
	})
	if err != nil {
		return err
	}
	a.ota.setWaiting(id, waiting)
	res.OTA = offer
	return nil
}

func (a *App) handleFirmwareDownload(w http.ResponseWriter, r *http.Request) {
	id := deviceIDFrom(r.Context())
	version := r.PathValue("version")
	if a.knobFW == nil {
		writeError(w, http.StatusServiceUnavailable, errFirmwareOff)
		return
	}
	if _, ok := a.knobFW.get(version); !ok {
		writeError(w, http.StatusNotFound, errFirmwareNotFound)
		return
	}
	now := a.devices.now().UTC()
	served := false
	o, _, err := a.devices.updateOTA(id, func(o *knobOTA, _ *deviceCheckin) (bool, error) {
		served = o.servable(version)
		if served && o.Phase == otaPhaseOffered {
			o.Phase, o.StartedAt = otaPhaseDownloading, &now
		}
		return false, nil
	})
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if !served {
		a.logger.InfoContext(r.Context(), "knob firmware download refused", "device_id", id, "version", version, "offered", o.Version, "phase", o.phase())
		writeError(w, http.StatusConflict, errOTANotOffered)
		return
	}
	meta, f, err := a.knobFW.open(version, firmwareBinName)
	if err != nil {
		a.writeFirmwareError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	a.ota.start(id, version, int64(meta.Size))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(otaWriteDeadline))
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("ETag", `"`+meta.SHA256+`"`)
	h.Set("Cache-Control", "no-store")
	a.logger.InfoContext(r.Context(), "knob firmware download", "device_id", id, "version", version, "range", r.Header.Get("Range"))
	cw := &otaCountingWriter{ResponseWriter: w, report: func(pos int64) { a.ota.advance(id, version, pos, a.devices.now()) }}
	http.ServeContent(cw, r, "", time.Time{}, f)
}

type otaCountingWriter struct {
	http.ResponseWriter
	offset int64
	sent   int64
	report func(int64)
}

func (c *otaCountingWriter) WriteHeader(code int) {
	if code == http.StatusPartialContent {
		spec, _ := strings.CutPrefix(c.Header().Get("Content-Range"), "bytes ")
		start, _, _ := strings.Cut(spec, "-")
		c.offset, _ = strconv.ParseInt(start, 10, 64)
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *otaCountingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.sent += int64(n)
	c.report(c.offset + c.sent)
	return n, err
}

func (c *otaCountingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (a *App) handleDeviceOTAGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	o, last, err := a.devices.otaSnapshot(id)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.otaStatus(id, o, last))
}

func (a *App) handleDeviceOTAPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode   *string         `json:"mode"`
		Target json.RawMessage `json:"target"`
		Retry  *bool           `json:"retry"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if req.Mode != nil && *req.Mode != otaModeManual && *req.Mode != otaModeAuto {
		a.writeDeviceError(w, r, fmt.Errorf("%w: mode must be manual or auto", errDeviceBody))
		return
	}
	var target *string
	clearTarget := string(req.Target) == "null"
	if len(req.Target) > 0 && !clearTarget {
		var v string
		if err := json.Unmarshal(req.Target, &v); err != nil {
			a.writeDeviceError(w, r, fmt.Errorf("%w: target must be a version string or null", errDeviceBody))
			return
		}
		if a.knobFW == nil {
			writeError(w, http.StatusServiceUnavailable, errFirmwareOff)
			return
		}
		if _, ok := a.knobFW.get(v); !ok {
			a.writeDeviceError(w, r, errOTAUnknownImage)
			return
		}
		target = &v
	}
	retry := req.Retry != nil && *req.Retry
	id := r.PathValue("id")
	o, last, err := a.devices.updateOTA(id, func(o *knobOTA, last *deviceCheckin) (bool, error) {
		return applyOTAPut(o, last, req.Mode, target, clearTarget, retry)
	})
	switch {
	case errors.Is(err, errNoRollback):
		writeError(w, http.StatusConflict, err)
		return
	case err != nil:
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "knob ota updated", "device_id", id, "mode", o.mode(), "target", o.Target, "retry", retry)
	writeJSON(w, http.StatusOK, a.otaStatus(id, o, last))
}

func applyOTAPut(o *knobOTA, last *deviceCheckin, mode, target *string, clearTarget, retry bool) (bool, error) {
	if (target != nil || retry) && (last == nil || last.OTA == nil || !last.OTA.Rollback) {
		return false, errNoRollback
	}
	if retry && target == nil && o.Target == "" && o.Version == "" {
		return false, fmt.Errorf("%w: nothing to retry", errDeviceBody)
	}
	bump := false
	if mode != nil && *mode != o.mode() {
		o.Mode, bump = *mode, true
	}
	if clearTarget && o.Target != "" {
		o.Target, o.Retry, bump = "", false, true
		if o.Phase == otaPhaseOffered {
			o.Phase = otaPhaseIdle
		}
	}
	if target != nil && (*target != o.Target || !otaActive(o.phase())) {
		o.Target, bump = *target, true
		if o.Version != *target || !otaActive(o.phase()) {
			o.Phase, o.Error, o.StartedAt, o.FinishedAt = otaPhaseIdle, "", nil, nil
		}
	}
	if retry {
		if o.Target == "" {
			o.Target = o.Version
		}
		o.Blocked = slices.DeleteFunc(o.Blocked, func(v string) bool { return v == o.Target })
		o.Retry, o.Phase, o.Error, o.StartedAt, o.FinishedAt = true, otaPhaseIdle, "", nil, nil
		bump = true
	}
	return bump, nil
}
