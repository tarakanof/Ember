package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Must stay below the server's 30 s Read/WriteTimeout.
	knobViewWaitMax          = 25 * time.Second
	knobViewWaitHeader       = "X-Ember-View-Wait"
	knobViewRecheck          = 5 * time.Second
	knobViewWaitersPerDevice = 2
	knobViewWaitersTotal     = 32
	knobViewWriteSlack       = 5 * time.Second
)

var errViewWaitInvalid = errors.New("wait must be whole seconds, 0-" + strconv.Itoa(int(knobViewWaitMax/time.Second)))

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

type viewWaiters struct {
	mu        sync.Mutex
	perDevice map[string]int
	total     int
}

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

func (v *viewWaiters) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.total
}

const (
	longPollChange   = "change"
	longPollTimeout  = "timeout"
	longPollShutdown = "shutdown"
	longPollGone     = "gone"
	longPollBusy     = "busy"
)

func (a *App) awaitKnobView(ctx context.Context, id, inm string, wait time.Duration) ([]byte, string, string, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	recheck := time.NewTicker(a.knobViewRecheckEvery())
	defer recheck.Stop()
	final := ""
	for {
		_, ch := a.changes.subscribe()
		now := a.viewNow()
		body, etag, err := a.knobView(id, now)
		switch {
		case err != nil:
			return nil, "", "", err
		case !etagMatches(inm, etag):
			return body, etag, longPollChange, nil
		case final != "":
			return body, etag, final, nil
		}
		if a.viewWaitHook != nil {
			a.viewWaitHook()
		}
		select {
		case <-ch:
		case <-a.quietEdge(now, wait):
		case <-recheck.C:
		case <-timer.C:
			final = longPollTimeout
		case <-a.changes.stopped():
			final = longPollShutdown
		case <-ctx.Done():
			return nil, "", longPollGone, nil
		}
	}
}

func inmHasStar(inm string) bool {
	for tag := range strings.SplitSeq(inm, ",") {
		if strings.TrimSpace(tag) == "*" {
			return true
		}
	}
	return false
}

func (a *App) viewNow() time.Time {
	if a.viewClock != nil {
		return a.viewClock()
	}
	return time.Now()
}

func (a *App) quietEdge(now time.Time, within time.Duration) <-chan time.Time {
	edge, ok := a.cfg.Load().nextQuietEdge(now)
	if !ok || edge.Sub(now) > within {
		return nil
	}
	return time.After(edge.Sub(now))
}

func (a *App) knobViewRecheckEvery() time.Duration {
	if a.viewRecheck > 0 {
		return a.viewRecheck
	}
	return knobViewRecheck
}

func (a *App) serveKnobViewWait(w http.ResponseWriter, r *http.Request, id string, wait time.Duration) bool {
	inm := r.Header.Get("If-None-Match")
	if wait <= 0 || inm == "" || inmHasStar(inm) {
		return false
	}
	if _, etag, err := a.knobView(id, a.viewNow()); err != nil || !etagMatches(inm, etag) {
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
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
	case err != nil:
		a.writeDeviceError(w, r, err)
	case result == longPollGone:
	default:
		a.writeKnobView(w, r, body, etag, a.viewNow())
	}
	return true
}
