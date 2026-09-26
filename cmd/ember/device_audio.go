package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

// deviceClientTimeout is the budget for one menu-initiated clock call, the
// same as the raw /v1/device proxy's.
const deviceClientTimeout = 8 * time.Second

// testChimeRTTTL is what "Play Test Chime" plays when no melody is named: a
// short rising arpeggio, distinct from the feature chimes so it can't be
// mistaken for a real alert.
const testChimeRTTTL = "test:d=16,o=6,b=180:c,e,g,8c7"

// melodyName is NG's rule for a stored melody's name (1–24 of [A-Za-z0-9_-]).
var melodyName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,24}$`)

// deviceClient returns an awtrix client for the currently-resolved clock.
func (a *App) deviceClient() (*awtrix.Client, error) {
	base, _, err := a.deviceBaseClient()
	if err != nil {
		return nil, err
	}
	return awtrix.NewClient(base, deviceClientTimeout), nil
}

// writeDeviceCallError maps an awtrix client error: a clock refusal goes
// through the same envelope relay as the raw proxy, anything else (no clock,
// network failure, undecodable reply) is 502.
func writeDeviceCallError(w http.ResponseWriter, err error) {
	var apiErr *awtrix.APIError
	if errors.As(err, &apiErr) {
		writeDeviceAPIError(w, apiErr)
		return
	}
	writeError(w, http.StatusBadGateway, err)
}

// callDevice runs one client call against the clock and answers 200 or the
// mapped error.
func (a *App) callDevice(w http.ResponseWriter, r *http.Request, call func(context.Context, *awtrix.Client) error) {
	cl, err := a.deviceClient()
	if err == nil {
		err = call(r.Context(), cl)
	}
	if err != nil {
		writeDeviceCallError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// audioUnavailable answers 503 — the status NG itself gives for an absent
// output — when the cached capabilities say the clock lacks what has needs.
// With nothing cached the request goes through and the clock decides.
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

// handleDevicePowerPut serves PUT /v1/device/display/power {"power":bool}:
// blanks or relights the matrix. It is its own route, not a key on
// PUT /v1/device/display, so an overlay edit can never blank the panel and a
// power toggle can never touch the overlay.
func (a *App) handleDevicePowerPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Power *bool `json:"power"`
	}
	if err := decodeJSON(w, r, &body, true); err != nil {
		writeError(w, http.StatusBadRequest, err)
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

// handleDeviceAudioTest serves POST /v1/device/audio/test: plays the built-in
// test chime, or {"melody":"<name>"} to preview a melody stored on the clock.
// It is an explicit user action, so it plays during quiet hours too; the
// clock's own soundEnabled mute still applies.
func (a *App) handleDeviceAudioTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Melody *string `json:"melody"`
	}
	if err := decodeJSON(w, r, &body, true); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
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

// handleDeviceAudioStop serves POST /v1/device/audio/stop: silences every
// output, radio included.
func (a *App) handleDeviceAudioStop(w http.ResponseWriter, r *http.Request) {
	if a.audioUnavailable(w, hasAnyAudio, "audio output") {
		return
	}
	a.callDevice(w, r, func(ctx context.Context, cl *awtrix.Client) error {
		return cl.StopAudio(ctx)
	})
}

// handleDeviceAudioMelodies serves GET /v1/device/audio/melodies: the melody
// files stored on the clock, in NG's own shape
// ({"melodies":[{name,rtttl,bytes,notes,durationMs,valid,error?,index?}],
// "usedBytes","totalBytes"}), for the menu's melody pickers.
func (a *App) handleDeviceAudioMelodies(w http.ResponseWriter, r *http.Request) {
	if a.audioUnavailable(w, hasBuzzer, "buzzer") {
		return
	}
	cl, err := a.deviceClient()
	if err != nil {
		writeDeviceCallError(w, err)
		return
	}
	list, err := cl.Melodies(r.Context())
	if err != nil {
		writeDeviceCallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
