package main

import (
	"errors"
	"net/url"
	"strconv"
	"time"
)

var errWatchedInvalid = errors.New("watched must be true or false")

func (a *App) musicWatched(now time.Time) (bool, time.Time) {
	watched := false
	var until time.Time
	a.devices.eachKnobCaps(func(id string, caps *deviceCaps) {
		at, ok := a.knobShown.freshUntil(id, knobNowPlayingPage, now)
		if !caps.hasFeature(featureShown) {
			at, ok = a.knobShown.polledUntil(id, now)
		}
		if ok {
			watched = true
			if at.After(until) {
				until = at
			}
		}
	})
	if a.watchedRead != nil {
		a.watchedRead()
	}
	return watched, until
}

func (a *App) musicWatchedUntil(q url.Values) (takeUntil, error) {
	if !q.Has("watched") {
		return nil, nil
	}
	known, err := strconv.ParseBool(q.Get("watched"))
	if err != nil {
		return nil, errWatchedInvalid
	}
	return func() (<-chan struct{}, <-chan time.Time, func(), bool) {
		wake := a.knobShown.changed()
		now := a.viewNow()
		watched, until := a.musicWatched(now)
		if watched != known {
			return nil, nil, func() {}, true
		}
		if until.IsZero() {
			return wake, nil, func() {}, false
		}
		recheck, stop := a.afterWatched(until.Sub(now) + time.Millisecond)
		return wake, recheck, stop, false
	}, nil
}

func (a *App) afterWatched(d time.Duration) (<-chan time.Time, func()) {
	if a.watchedAfter != nil {
		return a.watchedAfter(d)
	}
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}
