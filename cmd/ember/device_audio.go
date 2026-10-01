package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/tarakanof/ember/internal/awtrix"
)

const testChimeRTTTL = "test:d=16,o=6,b=180:c,e,g,8c7"

var melodyName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,24}$`)

func (a *App) callDevice(w http.ResponseWriter, r *http.Request, call func(context.Context, *awtrix.Client) error) {
	if err := a.clock.do(r.Context(), callMenu, call); err != nil {
		writeClockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) audioUnavailable(w http.ResponseWriter, has func(awtrix.AudioCaps) bool, what string) bool {
	caps, ok := a.capabilities()
	if !ok || has(caps.Audio) {
		return false
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "clock has no " + what,
		"code":  "unavailable",
	})
	return true
}

func hasBuzzer(c awtrix.AudioCaps) bool { return c.Buzzer }

func hasAnyAudio(c awtrix.AudioCaps) bool { return c.Buzzer || c.Track || c.MP3 || c.Radio }

func (a *App) handleDevicePowerPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Power *bool `json:"power"`
	}
	if !a.decodeOrReject(w, r, &body, true) {
		return
	}
	if body.Power == nil {
		writeError(w, http.StatusBadRequest, errors.New("power must be true or false"))
		return
	}
	a.callDevice(w, r, func(ctx context.Context, cl *awtrix.Client) error {
		return cl.SetDisplayPower(ctx, *body.Power)
	})
}

func (a *App) handleDeviceAudioTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Melody *string `json:"melody"`
	}
	if !a.decodeOptionalOrReject(w, r, &body, true) {
		return
	}
	if body.Melody != nil && !melodyName.MatchString(*body.Melody) {
		writeError(w, http.StatusBadRequest, errors.New("melody must be 1-24 of A-Z a-z 0-9 _ -"))
		return
	}
	if a.audioUnavailable(w, hasBuzzer, "buzzer") {
		return
	}
	a.callDevice(w, r, func(ctx context.Context, cl *awtrix.Client) error {
		if body.Melody != nil {
			return cl.PlayMelody(ctx, *body.Melody)
		}
		return cl.PlayRTTTL(ctx, testChimeRTTTL)
	})
}

func (a *App) handleDeviceAudioStop(w http.ResponseWriter, r *http.Request) {
	if a.audioUnavailable(w, hasAnyAudio, "audio output") {
		return
	}
	a.callDevice(w, r, func(ctx context.Context, cl *awtrix.Client) error {
		return cl.StopAudio(ctx)
	})
}

func (a *App) handleDeviceAudioMelodies(w http.ResponseWriter, r *http.Request) {
	if a.audioUnavailable(w, hasBuzzer, "buzzer") {
		return
	}
	var list awtrix.MelodyList
	err := a.clock.do(r.Context(), callMenu, func(ctx context.Context, cl *awtrix.Client) error {
		var err error
		list, err = cl.Melodies(ctx)
		return err
	})
	if err != nil {
		writeClockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
