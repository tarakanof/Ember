package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const defaultReminderSound = "remind:d=4,o=6,b=140:8e,8g,8c7"

const defaultReminderAlarm = "remind:d=4,o=6,b=140:8e,8g,8c7,1p"

const reminderHoldWindow = 15 * time.Minute

const reminderLoopCheckInterval = 15 * time.Second

type reminderFireRequest struct {
	Text         string `json:"text"`
	Sound        bool   `json:"sound"`
	Duration     int    `json:"duration"`
	NativeIconID string `json:"native_icon_id"`
	Hold         bool   `json:"hold"`
	RepeatSound  bool   `json:"repeat_sound"`
}

func (a *App) handleReminderFire(w http.ResponseWriter, r *http.Request) {
	var req reminderFireRequest
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200])
	}
	dur := req.Duration
	if dur < 1 || dur > 300 {
		dur = 8
	}
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
	a.logger.Info("reminder fire", "sound", req.Sound, "hold", req.Hold, "repeat_sound", req.RepeatSound,
		"duration", dur, "native_icon", req.NativeIconID != "")
	now := time.Now()
	if req.Hold {
		a.reminderHeldUntil.Store(now.Add(reminderHoldWindow).UnixNano())
	}
	payload := render.ReminderPopupPayload(text, req.NativeIconID, dur, req.Hold)
	payload["name"] = notifyNameReminder
	loop := req.Sound && req.Hold && req.RepeatSound
	switch {
	case loop:
		payload["soundRtttl"] = defaultReminderAlarm
		payload["soundLoop"] = true
	case req.Sound:
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
	if loop && !a.quietNow(now) {
		a.reminderLoop.arm(now.Add(reminderHoldWindow), payload)
	}
	w.WriteHeader(http.StatusNoContent)
}

type reminderLoop struct {
	mu      sync.Mutex
	until   time.Time
	payload map[string]any
	gen     int
}

func (l *reminderLoop) arm(until time.Time, payload map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.until, l.payload = until, payload
	l.gen++
}

func (l *reminderLoop) current() (until time.Time, payload map[string]any, gen int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.until, l.payload, l.gen
}

func (l *reminderLoop) clear(gen int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gen == gen {
		l.payload = nil
	}
}

func (a *App) quietNow(now time.Time) bool {
	enabled, start, end := a.cfg.Load().quietHoursWindow()
	return enabled && quietActive(start, end, now)
}

func (a *App) StartReminderLoopGuard(ctx context.Context) {
	t := time.NewTicker(reminderLoopCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.checkReminderLoop(ctx, now)
		}
	}
}

func (a *App) checkReminderLoop(ctx context.Context, now time.Time) {
	until, payload, gen := a.reminderLoop.current()
	if payload == nil {
		return
	}
	if a.reminderHeldUntil.Load() == 0 {
		a.reminderLoop.clear(gen)
		return
	}
	expired := !now.Before(until)
	quiet := a.quietNow(now)
	if !expired && !quiet {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.publisher.DismissNotifyByName(cctx, notifyNameReminder); err != nil && !isAPINotFound(err) {
		a.logger.Warn("reminder loop stop failed", "err", err)
		return
	}
	reason := "window"
	if !expired {
		reason = "quiet_hours"
		silent := make(map[string]any, len(payload))
		for k, v := range payload {
			if !slices.Contains(soundKeys, k) {
				silent[k] = v
			}
		}
		if err := a.publisher.Notify(cctx, silent); err != nil {
			a.logger.Warn("reminder silent re-push failed", "err", err)
		}
	}
	a.logger.Info("reminder loop stopped", "reason", reason)
	a.reminderLoop.clear(gen)
}

const reminderDedupeTTL = 10 * time.Minute

const maxReminderKeyLen = 256

type reminderDedupe struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

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

func (d *reminderDedupe) release(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.seen, key)
}

func (d *reminderDedupe) size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
