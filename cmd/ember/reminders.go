package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
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
		a.logger.Warn("reminder fire failed", "err", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
