package producer

import (
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const droppedFieldTTL = time.Hour

var unknownFieldRe = regexp.MustCompile(`unknown field \\?"([a-z_]+)\\?"`)

func unknownField(err error) (string, bool) {
	var se *ServerError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		return "", false
	}
	m := unknownFieldRe.FindStringSubmatch(se.Body)
	if m == nil {
		return "", false
	}
	return m[1], true
}

type fieldDropper[T any] struct {
	mu      sync.Mutex
	until   map[string]time.Time
	zeroers map[string]func(*T)
}

var optionalFields = &fieldDropper[StatusRequest]{
	until: map[string]time.Time{},
	zeroers: map[string]func(*StatusRequest){
		"project": func(r *StatusRequest) { r.Project = "" },
	},
}

var optionalUsageFields = &fieldDropper[UsageRequest]{
	until: map[string]time.Time{},
	zeroers: map[string]func(*UsageRequest){
		"account_key": func(r *UsageRequest) { r.AccountKey = "" },
		"plan":        func(r *UsageRequest) { r.Plan = "" },
		"window_minutes": func(r *UsageRequest) {
			r.FiveHour, r.SevenDay = withoutWindowMinutes(r.FiveHour), withoutWindowMinutes(r.SevenDay)
		},
	},
}

func withoutWindowMinutes(w *UsageWindow) *UsageWindow {
	if w == nil || w.WindowMinutes == 0 {
		return w
	}
	c := *w
	c.WindowMinutes = 0
	return &c
}

func (d *fieldDropper[T]) drop(field string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, known := d.zeroers[field]; !known {
		return false
	}
	if !now.Before(d.until[field]) {
		d.until[field] = now.Add(droppedFieldTTL)
	}
	return true
}

func (d *fieldDropper[T]) strip(req T, now time.Time) T {
	d.mu.Lock()
	defer d.mu.Unlock()
	for field, until := range d.until {
		if now.Before(until) {
			d.zeroers[field](&req)
		}
	}
	return req
}
