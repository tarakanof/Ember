package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	firmwareUploadDeadline = 2 * time.Minute
	firmwareELFDeadline    = 5 * time.Minute
)

func (a *App) firmwareStoreOr503(w http.ResponseWriter) (*firmwareStore, bool) {
	if a.knobFW == nil {
		writeError(w, http.StatusServiceUnavailable, errFirmwareOff)
		return nil, false
	}
	return a.knobFW, true
}

func (a *App) writeFirmwareError(w http.ResponseWriter, r *http.Request, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		a.rejectFirmware(w, r, http.StatusRequestEntityTooLarge, err)
	case errors.Is(err, errFirmwareNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, errFirmwareConflict), errors.Is(err, errFirmwareInUse):
		a.rejectFirmware(w, r, http.StatusConflict, err)
	case errors.Is(err, errFirmwareBadImage), errors.Is(err, errFirmwareWrongChip), errors.Is(err, errFirmwareWrongProject),
		errors.Is(err, errFirmwareBadVersion), errors.Is(err, errFirmwareDevSeed), errors.Is(err, errFirmwareELFMismatch):
		a.rejectFirmware(w, r, http.StatusBadRequest, err)
	default:
		a.logger.WarnContext(r.Context(), "knob firmware storage failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("firmware storage failed"))
	}
}

func (a *App) rejectFirmware(w http.ResponseWriter, r *http.Request, status int, err error) {
	a.logger.InfoContext(r.Context(), "knob firmware rejected", "path", r.URL.Path, "status", status, "err", err)
	writeError(w, status, err)
}

func (a *App) handleFirmwareUpload(w http.ResponseWriter, r *http.Request) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = firmwareChannelTest
	}
	if !validChannel(channel) {
		a.rejectFirmware(w, r, http.StatusBadRequest, errors.New("channel must be release or test"))
		return
	}
	replace := r.URL.Query().Get("replace") == "1"
	if r.ContentLength > firmwareMaxBytes {
		a.rejectFirmware(w, r, http.StatusRequestEntityTooLarge, fmt.Errorf("image larger than %d bytes", firmwareMaxBytes))
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(firmwareUploadDeadline))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, firmwareMaxBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if !errors.As(err, &tooBig) {
			err = fmt.Errorf("%w: %w", errFirmwareBadImage, err)
		}
		a.writeFirmwareError(w, r, err)
		return
	}
	desc, err := parseFirmwareImage(body)
	if err == nil && devSeedFound(body, a.cfg.Load().Auth.StatusToken) {
		err = errFirmwareDevSeed
	}
	if err != nil {
		a.writeFirmwareError(w, r, err)
		return
	}
	img, created, err := store.put(desc, body, channel, replace, a.devices.otaTargets, a.devices.otaKeeps, a.otaUnblocker(r.Context()))
	if err != nil && !created {
		a.writeFirmwareError(w, r, err)
		return
	}
	if err != nil {
		a.logger.WarnContext(r.Context(), "knob firmware cleanup incomplete", "version", img.Version, "err", err)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		a.logger.InfoContext(r.Context(), "knob firmware stored", "version", img.Version, "build", img.Build, "channel", img.Channel, "size", img.Size)
	}
	writeJSON(w, status, img)
}

func (a *App) handleFirmwareELFPut(w http.ResponseWriter, r *http.Request) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	if r.ContentLength > firmwareELFMaxBytes {
		a.rejectFirmware(w, r, http.StatusRequestEntityTooLarge, fmt.Errorf("elf larger than %d bytes", firmwareELFMaxBytes))
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(firmwareELFDeadline))
	version := r.PathValue("version")
	err := store.putELF(version, http.MaxBytesReader(w, r.Body, firmwareELFMaxBytes), a.cfg.Load().Auth.StatusToken)
	if err != nil {
		a.writeFirmwareError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "knob firmware elf stored", "version", version)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleFirmwareList(w http.ResponseWriter, r *http.Request) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, store.list())
}

func (a *App) handleFirmwareBin(w http.ResponseWriter, r *http.Request) {
	a.serveFirmwareFile(w, r, r.PathValue("version"), firmwareBinName)
}

func (a *App) handleFirmwareELF(w http.ResponseWriter, r *http.Request) {
	a.serveFirmwareFile(w, r, r.PathValue("version"), firmwareELFName)
}

func (a *App) handleFirmwareELFByBuild(w http.ResponseWriter, r *http.Request) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	build := r.PathValue("build")
	m, ok := store.byBuild(build)
	if !firmwareBuildPattern.MatchString(build) || !ok {
		writeError(w, http.StatusNotFound, errFirmwareNotFound)
		return
	}
	a.serveFirmwareFile(w, r, m.Version, firmwareELFName)
}

func (a *App) serveFirmwareFile(w http.ResponseWriter, r *http.Request, version, name string) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	m, f, err := store.open(version, name)
	if err != nil {
		a.writeFirmwareError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	ext := ".bin"
	if name == firmwareELFName {
		ext = ".elf"
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(firmwareELFDeadline))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="cinder-`+m.Version+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (a *App) handleFirmwarePatch(w http.ResponseWriter, r *http.Request) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if !validChannel(req.Channel) {
		a.rejectFirmware(w, r, http.StatusBadRequest, errors.New("channel must be release or test"))
		return
	}
	img, err := store.setChannel(r.PathValue("version"), req.Channel)
	if err != nil {
		a.writeFirmwareError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "knob firmware channel set", "version", img.Version, "channel", img.Channel)
	writeJSON(w, http.StatusOK, img)
}

func (a *App) handleFirmwareDelete(w http.ResponseWriter, r *http.Request) {
	store, ok := a.firmwareStoreOr503(w)
	if !ok {
		return
	}
	version := r.PathValue("version")
	forget := func(versions []string) {
		if err := a.devices.otaForget(versions); err != nil {
			a.logger.WarnContext(r.Context(), "knob ota state not cleared for deleted firmware", "versions", versions, "err", err)
		}
	}
	if err := store.remove(version, a.devices.otaTargets, forget); err != nil {
		a.writeFirmwareError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "knob firmware deleted", "version", version)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) otaUnblocker(ctx context.Context) func([]string) {
	return func(versions []string) {
		if err := a.devices.otaUnblock(versions); err != nil {
			a.logger.WarnContext(ctx, "knob ota blocked lists not pruned", "versions", versions, "err", err)
		}
	}
}
