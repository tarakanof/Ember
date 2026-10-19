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
	mu   sync.Mutex
	byID map[string]knobShown
}

func (s *knobShownStore) set(id string, v knobShown) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]knobShown{}
	}
	s.byID[id] = v
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
	s.mu.Unlock()
}

func (a *App) noteKnobShown(id string, r *http.Request) {
	q := r.URL.Query()
	if !q.Has("shown") {
		return
	}
	raw := q.Get("shown")
	s, ok := parseKnobShown(raw, a.viewNow())
	has := a.devices.lockedIfKnobHas(id, featureShown, func() {
		if a.shownSetHook != nil {
			a.shownSetHook()
		}
		if ok {
			a.knobShown.set(id, s)
		}
	})
	switch {
	case !has:
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
