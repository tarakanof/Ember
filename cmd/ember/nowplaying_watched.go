package main

import (
	"errors"
	"net/url"
	"strconv"
	"time"
)

var errWatchedInvalid = errors.New("watched must be true or false")

func (a *App) musicWatched(now time.Time) (bool, time.Time) {
	reporters, legacy, watched := 0, false, false
	var until time.Time
	a.devices.eachKnobCaps(func(id string, caps *deviceCaps) {
		if !caps.hasFeature(featureShown) {
			legacy = true
			return
		}
		reporters++
		if at, ok := a.knobShown.freshUntil(id, knobNowPlayingPage, now); ok {
			watched = true
			if at.After(until) {
				until = at
			}
		}
	})
	if legacy || reporters == 0 {
		return true, time.Time{}
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
	return func() (<-chan struct{}, <-chan time.Time, bool) {
		wake := a.knobShown.changed()
		now := a.viewNow()
		watched, until := a.musicWatched(now)
		if watched != known {
			return nil, nil, true
		}
		if until.IsZero() {
			return wake, nil, false
		}
		return wake, a.afterWatched(until.Sub(now) + time.Millisecond), false
	}, nil
}

func (a *App) afterWatched(d time.Duration) <-chan time.Time {
	if a.watchedAfter != nil {
		return a.watchedAfter(d)
	}
	return time.After(d)
}
