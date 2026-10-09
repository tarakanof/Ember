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

func (a *App) pomodoroBusy(now time.Time) bool { return a.pomodoroState(now).busy() }

func (a *App) otaCheckin(id string, report deviceCheckin, res *checkinResult) error {
	if a.knobFW == nil {
		return nil
	}
	gen, epoch := a.devices.otaGens()
	snap, _, err := a.devices.otaSnapshot(id)
	if err != nil {
		return err
	}
	in := otaInput{
		report:   report,
		now:      a.devices.now().UTC(),
		pomodoro: a.pomodoroBusy(time.Now()),
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
	if a.otaReadHook != nil {
		a.otaReadHook()
	}
	var offer *otaOffer
	var waiting string
	_, _, err = a.devices.updateOTA(id, func(o *knobOTA, _ *deviceCheckin) (bool, error) {
		offer, waiting = nil, ""
		if o.Target != snap.Target || o.mode() != snap.mode() || o.Attempt != snap.Attempt || a.devices.fwGen != gen {
			applyOTAResult(o, report, in.now)
			return false, nil
		}
		if o.Target != "" && in.target == nil && !otaActive(o.Phase) && a.devices.state.Epoch == epoch {
			o.Target, o.Retry = "", false
			return true, nil
		}
		offer, waiting = stepOTA(o, in)
		if offer == nil || o.Phase != otaPhaseOffered {
			a.ota.notOffered(id)
			return false, nil
		}
		window := otaNotStarted
		if offer.Auto {
			window = otaAutoNotStarted
		}
		if in.now.Sub(a.ota.offeredSince(id, o.Attempt, in.now)) >= window {
			o.Phase, o.Error, o.FinishedAt = otaPhaseFailed, "not_started", &in.now
			if offer.Auto {
				o.block()
			}
			offer, waiting = nil, ""
			a.ota.notOffered(id)
		}
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
	served, fresh := false, false
	o, _, err := a.devices.updateOTA(id, func(o *knobOTA, _ *deviceCheckin) (bool, error) {
		served = o.servable(version)
		if served && o.Phase == otaPhaseOffered {
			o.Phase, o.StartedAt, fresh = otaPhaseDownloading, &now, true
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
	a.ota.start(id, version, int64(meta.Size), fresh)
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
		target = &v
	}
	retry := req.Retry != nil && *req.Retry
	if (target != nil || retry) && a.knobFW == nil {
		writeError(w, http.StatusServiceUnavailable, errFirmwareOff)
		return
	}
	id := r.PathValue("id")
	var o knobOTA
	var last *deviceCheckin
	var err error
	for range otaPutTries {
		o, last, err = a.putOTA(id, req.Mode, target, clearTarget, retry)
		if !errors.Is(err, errFirmwareChanged) {
			break
		}
	}
	switch {
	case errors.Is(err, errNoRollback), errors.Is(err, errOTAInProgress), errors.Is(err, errFirmwareChanged):
		writeError(w, http.StatusConflict, err)
		return
	case err != nil:
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "knob ota updated", "device_id", id, "mode", o.mode(), "target", o.Target, "retry", retry)
	writeJSON(w, http.StatusOK, a.otaStatus(id, o, last))
}

func (a *App) putOTA(id string, mode, target *string, clearTarget, retry bool) (knobOTA, *deviceCheckin, error) {
	gen := a.devices.firmwareGen()
	snap, _, err := a.devices.otaSnapshot(id)
	if err != nil {
		return knobOTA{}, nil, err
	}
	want := otaWants(snap, target, retry)
	if want != "" {
		if _, ok := a.knobFW.get(want); !ok {
			return knobOTA{}, nil, errOTAUnknownImage
		}
	}
	if a.otaReadHook != nil {
		a.otaReadHook()
	}
	return a.devices.updateOTA(id, func(o *knobOTA, last *deviceCheckin) (bool, error) {
		if otaWants(*o, target, retry) != want || a.devices.fwGen != gen {
			return false, errFirmwareChanged
		}
		return applyOTAPut(o, last, mode, target, clearTarget, retry)
	})
}

func otaWants(o knobOTA, target *string, retry bool) string {
	switch {
	case target != nil:
		return *target
	case !retry:
		return ""
	case o.Target != "":
		return o.Target
	}
	return o.Version
}

func applyOTAPut(o *knobOTA, last *deviceCheckin, mode, target *string, clearTarget, retry bool) (bool, error) {
	if (target != nil || retry) && (last == nil || last.OTA == nil || !last.OTA.Rollback) {
		return false, errNoRollback
	}
	if retry && target == nil && o.Target == "" && o.Version == "" {
		return false, fmt.Errorf("%w: nothing to retry", errDeviceBody)
	}
	committed := o.Phase == otaPhaseInstalling || o.Phase == otaPhaseRestarting || o.Phase == otaPhaseVerifying
	if committed && (retry || (clearTarget && o.Target != "") || (target != nil && *target != o.Version)) {
		return false, errOTAInProgress
	}
	if clearTarget && o.Target != "" && o.Phase == otaPhaseDownloading {
		return false, errOTAInProgress
	}
	if target != nil && ((*target == o.Version && (o.Phase == otaPhaseFailed || o.Phase == otaPhaseRolledBack)) || slices.Contains(o.Blocked, *target)) {
		retry = true
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
	if clearTarget && (o.Phase == otaPhaseFailed || o.Phase == otaPhaseRolledBack) {
		o.Phase, o.Error = otaPhaseIdle, ""
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
