package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	stateFeedRoute = "state"
	pomoFeedRoute  = "pomodoro"
)

type stateBuild func() (body []byte, etag string, err error)

func fnvETag(sum uint64) string { return fmt.Sprintf(`"%016x"`, sum) }

func jsonLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func (s moodState) etag() string {
	sessions := slices.Clone(s.Sessions)
	for i := range sessions {
		sessions[i].UpdatedAt = sessions[i].UpdatedAt.UTC().Truncate(time.Minute)
	}
	slices.SortFunc(sessions, func(a, b Session) int { return strings.Compare(a.Key(), b.Key()) })
	h := fnv.New64a()
	_ = json.NewEncoder(h).Encode(struct {
		Sessions []Session
		Render   Render
	}{sessions, s.Render})
	return fnvETag(h.Sum64())
}

func (s pomodoroState) etag() string {
	st := s.Status
	left := int64(st.RemainingSec)
	if s.Counting {
		left = s.EndsAt.Unix()
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%t|%t|%d|%d|%t|%d", st.Phase, st.Running, st.Paused, st.PlannedSec, st.Round, s.Counting, left)
	return fnvETag(h.Sum64())
}

func (a *App) stateReply() ([]byte, string, error) {
	mood := a.moodState()
	body, err := jsonLine(mood.snapshot())
	return body, mood.etag(), err
}

func (a *App) pomodoroReply() ([]byte, string, error) {
	ps := a.pomodoroState(a.viewNow())
	if !ps.On {
		return nil, "", errPomodoroDisabled
	}
	body, err := jsonLine(ps.Status)
	return body, ps.etag(), err
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	a.serveStateFeed(w, r, stateFeedRoute, func() ([]byte, string, error) {
		w.Header().Set(devicesEpochHeader, strconv.FormatUint(a.devices.epochValue(), 10))
		return a.stateReply()
	})
}

func (a *App) handlePomodoroState(w http.ResponseWriter, r *http.Request) {
	a.serveStateFeed(w, r, pomoFeedRoute, a.pomodoroReply)
}

func (a *App) serveStateFeed(w http.ResponseWriter, r *http.Request, route string, build stateBuild) {
	wait, err := parseViewWait(r.URL.Query().Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	inm := r.Header.Get("If-None-Match")
	body, etag, err := build()
	if err == nil && wait > 0 && inm != "" && !inmHasStar(inm) && etagMatches(inm, etag) {
		release, ok := a.stateWaiters.acquire(route + "|" + clientIP(r))
		if !ok {
			a.metrics.incStateLongPoll(longPollBusy)
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusTooManyRequests, errors.New("too many waiting state requests"))
			return
		}
		defer release()
		rc := http.NewResponseController(w)
		dl := time.Now().Add(wait + knobViewWriteSlack)
		_ = rc.SetWriteDeadline(dl)
		_ = rc.SetReadDeadline(dl)

		var result string
		result, err = a.awaitChange(r.Context(), wait, func() (bool, error) {
			var buildErr error
			body, etag, buildErr = build()
			return !etagMatches(inm, etag), buildErr
		})
		if result != "" {
			a.metrics.incStateLongPoll(result)
		}
		if result == longPollGone {
			return
		}
	}
	switch {
	case errors.Is(err, errPomodoroDisabled):
		writeError(w, http.StatusNotFound, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeStateFeed(w, inm, body, etag)
	}
}

func writeStateFeed(w http.ResponseWriter, inm string, body []byte, etag string) {
	h := w.Header()
	h.Set("ETag", "W/"+etag)
	h.Set("Cache-Control", "no-store")
	h.Set(knobViewWaitHeader, strconv.Itoa(int(knobViewWaitMax/time.Second)))
	if etagMatches(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) awaitChange(ctx context.Context, wait time.Duration, changed func() (bool, error)) (string, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	recheck := time.NewTicker(a.knobViewRecheckEvery())
	defer recheck.Stop()
	final := ""
	for {
		_, ch := a.changes.subscribe()
		moved, err := changed()
		switch {
		case err != nil:
			return "", err
		case moved:
			return longPollChange, nil
		case final != "":
			return final, nil
		}
		if a.viewWaitHook != nil {
			a.viewWaitHook()
		}
		select {
		case <-ch:
		case <-recheck.C:
		case <-timer.C:
			final = longPollTimeout
		case <-a.changes.stopped():
			final = longPollShutdown
		case <-ctx.Done():
			return longPollGone, nil
		}
	}
}
