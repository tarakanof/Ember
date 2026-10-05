package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Long-poll on the knob view (#235). GET /v1/devices/self/view?wait=N with
// If-None-Match blocks while the view still matches that tag, up to N
// seconds, and answers 200 with the new view as soon as it changes, else 304
// with the same ETag. Without wait (or without If-None-Match) it answers at
// once, as before.
const (
	// knobViewWaitMax caps ?wait=. Below the server's 30 s Read/WriteTimeout
	// and Ember's 120 s IdleTimeout, and far below the knob's HTTP timeout,
	// which the firmware sets above the wait it sends.
	knobViewWaitMax = 25 * time.Second
	// knobViewWaitHeader advertises long-poll support and the cap in whole
	// seconds on every view answer. A client sends ?wait= only after it has
	// seen it: an older server ignores the parameter and would answer at once.
	knobViewWaitHeader = "X-Ember-View-Wait"
	// knobViewRecheck re-reads the view while waiting even without a notify.
	// Some fields move with the clock alone (sun-schedule brightness, weather
	// going stale, live mode ending, a session aging out); events cover the rest.
	knobViewRecheck = 5 * time.Second
	// Waiters are bounded: per device (a knob holds one; a second covers a
	// reconnect while the server has not yet seen the old socket close) and
	// in total. Over a cap the request gets 429 + Retry-After.
	knobViewWaitersPerDevice = 2
	knobViewWaitersTotal     = 32
	// The write deadline covers the wait plus this much to send the answer.
	knobViewWriteSlack = 5 * time.Second
)

var errViewWaitInvalid = errors.New("wait must be whole seconds, 0-" + strconv.Itoa(int(knobViewWaitMax/time.Second)))

// parseViewWait reads ?wait= in seconds: absent or 0 = no wait; above the cap
// is clamped; anything else non-numeric or negative is an error.
func parseViewWait(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errViewWaitInvalid
	}
	d := time.Duration(n) * time.Second
	if d > knobViewWaitMax {
		d = knobViewWaitMax
	}
	return d, nil
}

// viewWaiters counts blocked view requests per device and in total.
type viewWaiters struct {
	mu        sync.Mutex
	perDevice map[string]int
	total     int
}

// acquire takes a waiter slot for id; release must be called once done.
func (v *viewWaiters) acquire(id string) (release func(), ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.perDevice == nil {
		v.perDevice = map[string]int{}
	}
	if v.perDevice[id] >= knobViewWaitersPerDevice || v.total >= knobViewWaitersTotal {
		return nil, false
	}
	v.perDevice[id]++
	v.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			v.mu.Lock()
			defer v.mu.Unlock()
			if v.perDevice[id]--; v.perDevice[id] <= 0 {
				delete(v.perDevice, id)
			}
			v.total--
		})
	}, true
}

// count is the number of blocked requests now (the waiters gauge).
func (v *viewWaiters) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.total
}

// longPoll results, for ember_knob_view_longpoll_total.
const (
	longPollChange   = "change"   // the view moved: 200
	longPollTimeout  = "timeout"  // wait ran out: 304
	longPollShutdown = "shutdown" // the server is stopping: 304 at once
	longPollGone     = "gone"     // the client went away: nothing written
	longPollBusy     = "busy"     // over a waiter cap: 429
)

// awaitKnobView blocks until id's view no longer matches inm, wait runs out,
// the server stops or ctx ends. It returns the view to answer with, its tag
// and the result; on ctx's end the body is nil.
func (a *App) awaitKnobView(ctx context.Context, id, inm string, wait time.Duration) ([]byte, string, string, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	recheck := time.NewTicker(a.knobViewRecheckEvery())
	defer recheck.Stop()
	final := ""
	for {
		// Subscribe before reading: a change after this point closes ch.
		_, ch := a.changes.subscribe()
		body, etag, err := a.knobView(id, time.Now())
		switch {
		case err != nil:
			return nil, "", "", err
		case !etagMatches(inm, etag):
			return body, etag, longPollChange, nil
		case final != "":
			return body, etag, final, nil
		}
		select {
		case <-ch:
		case <-recheck.C:
		case <-timer.C:
			final = longPollTimeout // one last read, then 304
		case <-a.changes.stopped():
			final = longPollShutdown
		case <-ctx.Done():
			return nil, "", longPollGone, nil
		}
	}
}

func (a *App) knobViewRecheckEvery() time.Duration {
	if a.viewRecheck > 0 {
		return a.viewRecheck
	}
	return knobViewRecheck
}

// serveKnobViewWait handles a long-poll request. It reports false when the
// request is not one (no wait or no tag), so the caller answers at once.
func (a *App) serveKnobViewWait(w http.ResponseWriter, r *http.Request, id string, wait time.Duration) bool {
	inm := r.Header.Get("If-None-Match")
	if wait <= 0 || inm == "" {
		return false
	}
	release, ok := a.viewWaiters.acquire(id)
	if !ok {
		a.metrics.incLongPoll(longPollBusy)
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusTooManyRequests, errors.New("too many waiting view requests"))
		return true
	}
	defer release()
	// The server's 30 s timeouts count from the request; give this one the
	// wait plus slack. A Read deadline past ReadTimeout also keeps net/http's
	// background read from cancelling the context early.
	rc := http.NewResponseController(w)
	dl := time.Now().Add(wait + knobViewWriteSlack)
	_ = rc.SetWriteDeadline(dl)
	_ = rc.SetReadDeadline(dl)

	body, etag, result, err := a.awaitKnobView(r.Context(), id, inm, wait)
	if result != "" {
		a.metrics.incLongPoll(result)
	}
	switch {
	case errors.Is(err, errDeviceNotFound):
		// Deleted while waiting: answer as the auth check would have.
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
	case err != nil:
		a.writeDeviceError(w, r, err)
	case result == longPollGone:
	default:
		a.writeKnobView(w, r, body, etag, time.Now())
	}
	return true
}
