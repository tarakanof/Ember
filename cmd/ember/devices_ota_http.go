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

	"github.com/tarakanof/ember/internal/devices"
)

const (
	firmwareDownloadBurst  = 6
	firmwareDownloadPerSec = 0.1
)

func (a *App) pomodoroBusy(now time.Time) bool { return a.pomodoroState(now).busy() }

func (a *App) otaCheckin(id string, report devices.Checkin, res *devices.CheckinResult) error {
	if a.knobFW == nil {
		return nil
	}
	gen, epoch := a.devices.OTAGens()
	snap, _, err := a.devices.OTASnapshot(id)
	if err != nil {
		return err
	}
	in := otaInput{
		report:   report,
		now:      a.devices.Now().UTC(),
		pomodoro: a.pomodoroBusy(time.Now()),
		coredump: res.CoredumpWanted != "",
	}
	if snap.Target != "" {
		if m, ok := a.knobFW.get(snap.Target); ok {
			in.target = &m
		}
	} else if snap.EffectiveMode() == otaModeAuto {
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
	var offer *devices.OTAOffer
	var waiting string
	_, _, err = a.devices.UpdateOTA(id, func(o *devices.KnobOTA, _ *devices.Checkin) (bool, error) {
		offer, waiting = nil, ""
		if o.Target != snap.Target || o.EffectiveMode() != snap.EffectiveMode() || o.Attempt != snap.Attempt || a.devices.FirmwareGenLocked() != gen {
			applyOTAResult(o, report, in.now)
			return false, nil
		}
		if o.Target != "" && in.target == nil && !devices.OTAActive(o.Phase) && a.devices.EpochLocked() == epoch {
			o.Target, o.Retry = "", false
			return true, nil
		}
		offer, waiting = stepOTA(o, in)
		if offer == nil || o.Phase != devices.OTAPhaseOffered {
			a.ota.notOffered(id)
			return false, nil
		}
		window := otaNotStarted
		if offer.Auto {
			window = otaAutoNotStarted
		}
		if in.now.Sub(a.ota.offeredSince(id, o.Attempt, in.now)) >= window {
			o.Phase, o.Error, o.FinishedAt = devices.OTAPhaseFailed, "not_started", &in.now
			if offer.Auto {
				o.Block()
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
	now := a.devices.Now().UTC()
	served, fresh := false, false
	o, _, err := a.devices.UpdateOTA(id, func(o *devices.KnobOTA, _ *devices.Checkin) (bool, error) {
		served = o.Servable(version)
		if served && o.Phase == devices.OTAPhaseOffered {
			o.Phase, o.StartedAt, fresh = devices.OTAPhaseDownloading, &now, true
		}
		return false, nil
	})
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if !served {
		a.logger.InfoContext(r.Context(), "knob firmware download refused", "device_id", id, "version", version, "offered", o.Version, "phase", o.EffectivePhase())
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
	cw := &otaCountingWriter{ResponseWriter: w, report: func(pos int64) { a.ota.advance(id, version, pos, a.devices.Now()) }}
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
	o, last, err := a.devices.OTASnapshot(id)
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
	if req.Mode != nil && *req.Mode != devices.OTAModeManual && *req.Mode != otaModeAuto {
		a.writeDeviceError(w, r, fmt.Errorf("%w: mode must be manual or auto", devices.ErrBody))
		return
	}
	var target *string
	clearTarget := string(req.Target) == "null"
	if len(req.Target) > 0 && !clearTarget {
		var v string
		if err := json.Unmarshal(req.Target, &v); err != nil {
			a.writeDeviceError(w, r, fmt.Errorf("%w: target must be a version string or null", devices.ErrBody))
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
	var o devices.KnobOTA
	var last *devices.Checkin
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
	a.logger.InfoContext(r.Context(), "knob ota updated", "device_id", id, "mode", o.EffectiveMode(), "target", o.Target, "retry", retry)
	writeJSON(w, http.StatusOK, a.otaStatus(id, o, last))
}

func (a *App) putOTA(id string, mode, target *string, clearTarget, retry bool) (devices.KnobOTA, *devices.Checkin, error) {
	gen := a.devices.FirmwareGen()
	snap, _, err := a.devices.OTASnapshot(id)
	if err != nil {
		return devices.KnobOTA{}, nil, err
	}
	want := otaWants(snap, target, retry)
	if want != "" {
		if _, ok := a.knobFW.get(want); !ok {
			return devices.KnobOTA{}, nil, errOTAUnknownImage
		}
	}
	if a.otaReadHook != nil {
		a.otaReadHook()
	}
	return a.devices.UpdateOTA(id, func(o *devices.KnobOTA, last *devices.Checkin) (bool, error) {
		if otaWants(*o, target, retry) != want || a.devices.FirmwareGenLocked() != gen {
			return false, errFirmwareChanged
		}
		return applyOTAPut(o, last, mode, target, clearTarget, retry)
	})
}

func otaWants(o devices.KnobOTA, target *string, retry bool) string {
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

func applyOTAPut(o *devices.KnobOTA, last *devices.Checkin, mode, target *string, clearTarget, retry bool) (bool, error) {
	if (target != nil || retry) && (last == nil || last.OTA == nil || !last.OTA.Rollback) {
		return false, errNoRollback
	}
	if retry && target == nil && o.Target == "" && o.Version == "" {
		return false, fmt.Errorf("%w: nothing to retry", devices.ErrBody)
	}
	committed := o.Phase == devices.OTAPhaseInstalling || o.Phase == devices.OTAPhaseRestarting || o.Phase == devices.OTAPhaseVerifying
	if committed && (retry || (clearTarget && o.Target != "") || (target != nil && *target != o.Version)) {
		return false, errOTAInProgress
	}
	if clearTarget && o.Target != "" && o.Phase == devices.OTAPhaseDownloading {
		return false, errOTAInProgress
	}
	if target != nil && ((*target == o.Version && (o.Phase == devices.OTAPhaseFailed || o.Phase == devices.OTAPhaseRolledBack)) || slices.Contains(o.Blocked, *target)) {
		retry = true
	}
	bump := false
	if mode != nil && *mode != o.EffectiveMode() {
		o.Mode, bump = *mode, true
	}
	if clearTarget && o.Target != "" {
		o.Target, o.Retry, bump = "", false, true
		if o.Phase == devices.OTAPhaseOffered {
			o.Phase = devices.OTAPhaseIdle
		}
	}
	if clearTarget && (o.Phase == devices.OTAPhaseFailed || o.Phase == devices.OTAPhaseRolledBack) {
		o.Phase, o.Error = devices.OTAPhaseIdle, ""
	}
	if target != nil && (*target != o.Target || !devices.OTAActive(o.EffectivePhase())) {
		o.Target, bump = *target, true
		if o.Version != *target || !devices.OTAActive(o.EffectivePhase()) {
			o.Phase, o.Error, o.StartedAt, o.FinishedAt = devices.OTAPhaseIdle, "", nil, nil
		}
	}
	if retry {
		if o.Target == "" {
			o.Target = o.Version
		}
		o.Blocked = slices.DeleteFunc(o.Blocked, func(v string) bool { return v == o.Target })
		o.Retry, o.Phase, o.Error, o.StartedAt, o.FinishedAt = true, devices.OTAPhaseIdle, "", nil, nil
		bump = true
	}
	return bump, nil
}
