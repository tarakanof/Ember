package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// defaultReminderSound is a short gentle RTTTL chime for reminders that opt into
// sound (the TC001 piezo is RTTTL-only). It rides on the notification's own
// soundRtttl key — NG plays a notification's melody alongside its draw/icon, so
// the AWTRIX3-era out-of-band /api/rtttl call is gone.
const defaultReminderSound = "remind:d=4,o=6,b=140:8e,8g,8c7"

// reminderFireRequest is the body of POST /v1/reminders/fire. The macOS app (which
// watches Apple Reminders via EventKit) sends it when a reminder comes due; the
// server only renders + pushes the bell popup. The server holds no reminder
// config — all scheduling/settings live in the menu app.
type reminderFireRequest struct {
	Text         string `json:"text"`
	Sound        bool   `json:"sound"`
	Duration     int    `json:"duration"`
	NativeIconID string `json:"native_icon_id"`
	// Hold makes the alarm take over the display until the user dismisses it
	// (middle button) rather than auto-dismissing after Duration.
	Hold bool `json:"hold"`
}

// handleReminderFire renders a bell-icon popup for an Apple Reminder that has come
// due and pushes it to the device. Stateless: there is no server-side reminder
// list or schedule anymore.
func (a *App) handleReminderFire(w http.ResponseWriter, r *http.Request) {
	var req reminderFireRequest
	if err := decodeJSON(w, r, &req, true); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	// Cap on a rune boundary — slicing raw bytes could split a multi-byte UTF-8
	// rune and emit invalid UTF-8, which AWTRIX 0.98 rejects with ErrorParsingJson.
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200])
	}
	dur := req.Duration
	if dur < 1 || dur > 300 {
		dur = 8
	}
	// The app retries a fire it can't prove was delivered; the key makes that
	// retry a no-op if the first attempt reached (or is still reaching) the clock.
	// Checked before arming the hold window so a duplicate changes nothing.
	key := r.Header.Get("Idempotency-Key")
	if key != "" && len(key) <= maxReminderKeyLen {
		if !a.reminderKeys.claim(key, time.Now()) {
			a.logger.Info("reminder fire duplicate", "key", key)
			w.WriteHeader(http.StatusOK)
			return
		}
	} else {
		key = ""
	}
	a.logger.Info("reminder fire", "sound", req.Sound, "hold", req.Hold, "duration", dur, "native_icon", req.NativeIconID != "")
	// While a hold:true alarm is on the clock, the device's button callback would
	// otherwise start Pomodoro when the user presses a button to dismiss it. Arm a
	// window so handleAwtrixButton treats that press as an acknowledgement instead.
	if req.Hold {
		a.reminderHeldUntil.Store(time.Now().Add(15 * time.Minute).UnixNano())
	}
	payload := render.ReminderPopupPayload(text, req.NativeIconID, dur, req.Hold)
	payload["name"] = notifyNameReminder
	if req.Sound {
		payload["soundRtttl"] = defaultReminderSound
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := a.publisher.Notify(ctx, payload); err != nil {
		if key != "" {
			a.reminderKeys.release(key)
		}
		a.logger.Warn("reminder fire failed", "err", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reminderDedupeTTL is how long a fired reminder key is remembered: well past
// the app's 90s fire window, so every retry of one occurrence is caught.
const reminderDedupeTTL = 10 * time.Minute

// maxReminderKeyLen bounds a client-supplied key; longer keys are ignored.
const maxReminderKeyLen = 256

// reminderDedupe remembers recently fired reminder idempotency keys so a retried
// POST /v1/reminders/fire doesn't ring the same occurrence twice. The zero value
// is ready to use.
type reminderDedupe struct {
	mu   sync.Mutex // protects seen
	seen map[string]time.Time
}

// claim records key at now and reports true, or reports false when key was
// claimed within the TTL. Expired keys are pruned on each call.
func (d *reminderDedupe) claim(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, at := range d.seen {
		if now.Sub(at) >= reminderDedupeTTL {
			delete(d.seen, k)
		}
	}
	if _, ok := d.seen[key]; ok {
		return false
	}
	if d.seen == nil {
		d.seen = make(map[string]time.Time)
	}
	d.seen[key] = now
	return true
}

// release forgets key, so a fire whose push failed can be retried.
func (d *reminderDedupe) release(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.seen, key)
}

// size returns the number of remembered keys.
func (d *reminderDedupe) size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
