package main

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

var errCoredumpsOff = errors.New("core dump storage unavailable")

func (a *App) coredumpReply(device string, diag *deviceDiag, res *checkinResult) {
	if a.coredumps == nil || diag == nil || diag.Crash == nil || diag.Crash.ID == "" {
		return
	}
	if a.coredumps.has(device, diag.Crash.ID) {
		res.CoredumpAck = diag.Crash.ID
		return
	}
	res.CoredumpWanted = diag.Crash.ID
}

func (a *App) handleCoredumpUpload(w http.ResponseWriter, r *http.Request) {
	device := deviceIDFrom(r.Context())
	id := r.URL.Query().Get("id")
	if !coredumpIDPattern.MatchString(id) {
		a.rejectCoredump(w, r, http.StatusBadRequest, "id", errors.New("id must be 8 lower-case hex digits"))
		return
	}
	store := a.coredumps
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, errCoredumpsOff)
		return
	}
	if r.ContentLength > coredumpMaxBytes {
		a.rejectCoredump(w, r, http.StatusRequestEntityTooLarge, "too_large", errors.New("core dump larger than 131072 bytes"))
		return
	}
	if store.has(device, id) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !store.begin(device) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusConflict, errCoredumpBusy)
		return
	}
	defer store.end(device)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, coredumpMaxBytes))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		a.rejectCoredump(w, r, http.StatusRequestEntityTooLarge, "too_large", errors.New("core dump larger than 131072 bytes"))
		return
	}
	if err != nil {
		a.rejectCoredump(w, r, http.StatusBadRequest, "read", err)
		return
	}
	sum, err := coredumpID(body)
	if err == nil && sum != id {
		err = errors.New("id does not match the core dump checksum")
	}
	if err != nil {
		a.rejectCoredump(w, r, http.StatusBadRequest, "checksum", err)
		return
	}
	meta := coredumpMeta{ID: id, Size: len(body)}
	if _, c, err := a.devices.diagnostics(device); err == nil && c != nil {
		meta.FW = c.FW
		if c.Diag != nil && c.Diag.Crash != nil && c.Diag.Crash.ID == id {
			meta.Reason, meta.Task, meta.PC, meta.ELF = c.Diag.Crash.Reason, c.Diag.Crash.Task, c.Diag.Crash.PC, c.Diag.Crash.ELF
		}
	}
	err = store.put(device, meta, body, func() bool {
		_, _, err := a.devices.diagnostics(device)
		return err == nil
	})
	if errors.Is(err, errCoredumpGone) {
		writeError(w, http.StatusNotFound, errDeviceNotFound)
		return
	}
	if err != nil {
		a.logger.WarnContext(r.Context(), "knob coredump write failed", "device_id", device, "dump_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("core dump write failed"))
		return
	}
	a.logger.InfoContext(r.Context(), "knob coredump stored", "device_id", device, "dump_id", id, "size", len(body), "fw", meta.FW)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) rejectCoredump(w http.ResponseWriter, r *http.Request, status int, reason string, err error) {
	a.logger.InfoContext(r.Context(), "knob coredump rejected",
		"device_id", deviceIDFrom(r.Context()), "reason", reason, "err", err)
	writeError(w, status, err)
}

func (a *App) coredumpOwner(w http.ResponseWriter, r *http.Request) (string, *coredumpStore, bool) {
	device := r.PathValue("id")
	if _, _, err := a.devices.diagnostics(device); err != nil {
		a.writeDeviceError(w, r, err)
		return "", nil, false
	}
	if a.coredumps == nil {
		writeError(w, http.StatusServiceUnavailable, errCoredumpsOff)
		return "", nil, false
	}
	return device, a.coredumps, true
}

func (a *App) writeCoredumpError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errCoredumpNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	a.logger.WarnContext(r.Context(), "knob coredump read failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, errors.New("core dump storage failed"))
}

func (a *App) handleCoredumpList(w http.ResponseWriter, r *http.Request) {
	device, store, ok := a.coredumpOwner(w, r)
	if !ok {
		return
	}
	list, err := store.list(device)
	if err != nil {
		a.writeCoredumpError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *App) handleCoredumpGet(w http.ResponseWriter, r *http.Request) {
	device, store, ok := a.coredumpOwner(w, r)
	if !ok {
		return
	}
	meta, body, err := store.open(device, r.PathValue("dump"))
	if err != nil {
		a.writeCoredumpError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Content-Disposition", `attachment; filename="`+coredumpFilename(device, meta)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func (a *App) handleCoredumpDelete(w http.ResponseWriter, r *http.Request) {
	device, store, ok := a.coredumpOwner(w, r)
	if !ok {
		return
	}
	if err := store.remove(device, r.PathValue("dump")); err != nil {
		a.writeCoredumpError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "knob coredump deleted", "device_id", device, "dump_id", r.PathValue("dump"))
	w.WriteHeader(http.StatusNoContent)
}

func coredumpFilename(device string, meta coredumpMeta) string {
	fw := strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
			return c
		}
		return '_'
	}, meta.FW)
	if fw == "" {
		fw = "unknown"
	}
	return device + "-" + fw + "-" + meta.ID + ".bin"
}
