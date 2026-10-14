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

type fieldDropper struct {
	mu      sync.Mutex
	until   map[string]time.Time
	zeroers map[string]func(*StatusRequest)
}

var optionalFields = &fieldDropper{
	until: map[string]time.Time{},
	zeroers: map[string]func(*StatusRequest){
		"project": func(r *StatusRequest) { r.Project = "" },
	},
}

func (d *fieldDropper) drop(field string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, known := d.zeroers[field]; !known || now.Before(d.until[field]) {
		return false
	}
	d.until[field] = now.Add(droppedFieldTTL)
	return true
}

func (d *fieldDropper) strip(req StatusRequest, now time.Time) StatusRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	for field, until := range d.until {
		if now.Before(until) {
			d.zeroers[field](&req)
		}
	}
	return req
}
