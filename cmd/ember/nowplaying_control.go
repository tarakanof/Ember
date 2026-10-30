package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/devices"
	"github.com/tarakanof/ember/internal/nowplaying"
)

const (
	maxControlKey   = 128
	commandsWaitMax = 25 * time.Second
)

var (
	errNothingPlaying = errors.New("nothing is playing")
	errShownChanged   = errors.New("the shown track changed")
	errCommandsMaster = errors.New("the Music command queue needs EMBER_TOKEN")
)

func (a *App) handleNowPlayingControl(w http.ResponseWriter, r *http.Request) {
	var req nowplaying.ControlRequest
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	caller := "owner"
	if id := deviceIDFrom(r.Context()); id != "" {
		caller = "device:" + id
		cfg, _, err := a.devices.Config(id)
		if err != nil {
			a.writeDeviceError(w, r, err)
			return
		}
		if !cfg.PageOn(devices.KnobNowPlayingPage) {
			writeError(w, http.StatusForbidden, errors.New("the device's now-playing page is off"))
			return
		}
	}
	now := time.Now()
	key := r.Header.Get("Idempotency-Key")
	if len(key) > maxControlKey {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Idempotency-Key is longer than %d bytes", maxControlKey))
		return
	}
	if !a.nowPlaying.ControlKeys.Claim(caller, key, now) {
		a.logger.Info("now-playing control duplicate", "action", req.Action, "caller", caller)
		writeJSON(w, http.StatusOK, nowplaying.ControlResult{Status: "duplicate"})
		return
	}
	if !a.nowPlaying.ControlLimit.AllowNow(caller) {
		a.nowPlaying.ControlKeys.Release(caller, key)
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, errors.New("control requests too fast"))
		return
	}
	e, ok := a.nowPlaying.Reg.Current(now)
	if !ok || !req.Matches(e) {
		a.nowPlaying.ControlKeys.Release(caller, key)
		err := errNothingPlaying
		if ok {
			err = errShownChanged
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*nowplaying.PlexTimeout)
	defer cancel()
	res, err := a.nowPlaying.Control(ctx, e, req)
	a.logger.Info("now-playing control", "action", req.Action, "delta", req.Delta, "source", e.Source,
		"caller", caller, "status", res.Status, "err", err)
	switch {
	case errors.Is(err, nowplaying.ErrNoController):
		a.nowPlaying.ControlKeys.Release(caller, key)
		writeError(w, http.StatusServiceUnavailable, err)
	case err != nil:
		// The key stays claimed: the player may have taken it, so a retry must not skip twice.
		writeError(w, http.StatusBadGateway, err)
	case res.Status == "queued":
		writeJSON(w, http.StatusAccepted, res)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

type commandsAnswer struct {
	Commands []nowplaying.QueuedCommand `json:"commands"`
	Watched  bool                       `json:"watched"`
}

func (a *App) handleNowPlayingCommands(w http.ResponseWriter, r *http.Request) {
	if !a.masterOnly(w, r, errCommandsMaster) {
		return
	}
	q := r.URL.Query()
	player := q.Get("player")
	if player == "" || !utf8.ValidString(player) || utf8.RuneCountInString(player) > 64 {
		writeError(w, http.StatusBadRequest, errors.New("player is required (at most 64 characters)"))
		return
	}
	wait, err := parseViewWait(q.Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if wait > commandsWaitMax {
		wait = commandsWaitMax
	}
	until, err := a.musicWatchedUntil(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if wait > 0 {
		rc := http.NewResponseController(w)
		dl := time.Now().Add(wait + knobViewWriteSlack)
		_ = rc.SetWriteDeadline(dl)
		_ = rc.SetReadDeadline(dl)
	}
	cmds, err := a.nowPlaying.Commands.Take(r.Context(), player, wait, until)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, err)
		return
	}
	if cmds == nil {
		cmds = []nowplaying.QueuedCommand{}
	}
	w.Header().Set("Cache-Control", "no-store")
	watched, _ := a.musicWatched(a.viewNow())
	writeJSON(w, http.StatusOK, commandsAnswer{Commands: cmds, Watched: watched})
}
