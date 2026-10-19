package main

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const knobShownFresh = 2 * knobViewWaitMax

var knobShownPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}(:[a-z]{1,8})?$`)

type knobShown struct {
	Page string    `json:"page"`
	Face *string   `json:"face"`
	At   time.Time `json:"at"`
}

func parseKnobShown(raw string, at time.Time) (knobShown, bool) {
	if !knobShownPattern.MatchString(raw) {
		return knobShown{}, false
	}
	s := knobShown{At: at}
	page, face, ok := strings.Cut(raw, ":")
	s.Page = page
	if ok {
		s.Face = &face
	}
	return s, true
}

type knobShownStore struct {
	mu      sync.Mutex
	byID    map[string]knobShown
	polled  map[string]time.Time
	wake    chan struct{}
	setHook func()
}

func (s *knobShownStore) poll(id string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.polled == nil {
		s.polled = map[string]time.Time{}
	}
	prev, ok := s.polled[id]
	s.polled[id] = at
	if !ok || at.Sub(prev) > knobShownFresh {
		s.bumpLocked()
	}
}

func (s *knobShownStore) polledUntil(id string, now time.Time) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.polled[id]
	if !ok || now.Sub(at) > knobShownFresh {
		return time.Time{}, false
	}
	return at.Add(knobShownFresh), true
}

func (s *knobShownStore) set(id string, v knobShown) {
	if s.setHook != nil {
		s.setHook()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]knobShown{}
	}
	prev, ok := s.byID[id]
	s.byID[id] = v
	if !ok || prev.Page != v.Page || v.At.Sub(prev.At) > knobShownFresh {
		s.bumpLocked()
	}
}

func (s *knobShownStore) changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{})
	}
	return s.wake
}

func (s *knobShownStore) bump() {
	s.mu.Lock()
	s.bumpLocked()
	s.mu.Unlock()
}

func (s *knobShownStore) bumpLocked() {
	if s.wake != nil {
		close(s.wake)
		s.wake = nil
	}
}

func (s *knobShownStore) freshUntil(id, page string, now time.Time) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[id]
	if !ok || v.Page != page || now.Sub(v.At) > knobShownFresh {
		return time.Time{}, false
	}
	return v.At.Add(knobShownFresh), true
}

func (s *knobShownStore) fresh(id string, now time.Time) *knobShown {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[id]
	if !ok || now.Sub(v.At) > knobShownFresh {
		return nil
	}
	v.At = v.At.UTC().Truncate(time.Second)
	if v.Face != nil {
		f := *v.Face
		v.Face = &f
	}
	return &v
}

func (s *knobShownStore) forget(id string) {
	s.mu.Lock()
	delete(s.byID, id)
	delete(s.polled, id)
	s.mu.Unlock()
}

func (a *App) noteKnobShown(id string, r *http.Request) {
	q := r.URL.Query()
	now := a.viewNow()
	raw, report := q.Get("shown"), q.Has("shown")
	s, ok := parseKnobShown(raw, now)
	has := false
	a.devices.lockedKnob(id, func(caps *deviceCaps) {
		a.knobShown.poll(id, now)
		if !report || !caps.hasFeature(featureShown) {
			return
		}
		has = true
		if a.shownSetHook != nil {
			a.shownSetHook()
		}
		if ok {
			a.knobShown.set(id, s)
		}
	})
	switch {
	case !report, !has:
	case !ok:
		if a.shownDrops.changed(id, "malformed") {
			a.logger.InfoContext(r.Context(), "knob shown ignored", "device_id", id, "reason", "malformed", "bytes", len(raw))
		}
	default:
		a.shownDrops.changed(id, "")
	}
}

func (a *App) withShown(v deviceView) deviceView {
	if v.EffectiveCaps != nil && slices.Contains(v.EffectiveCaps.Features, featureShown) {
		v.Shown = a.knobShown.fresh(v.ID, a.viewNow())
	}
	return v
}
