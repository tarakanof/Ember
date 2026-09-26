package main

import (
	"context"
	"slices"
	"time"
)

// soundKeys are awtrix-ng's three notification sound fields (AWTRIX3 spelled the
// latter two `rtttl` and `loopSound`). quietPublisher strips all of them.
var soundKeys = []string{"sound", "soundRtttl", "soundLoop"}

// quietPublisher gates all device audio behind the quiet-hours window. During
// the window, Notify payloads lose their sound keys and PlayRTTTL succeeds
// without calling the device; everything else delegates unchanged. Enforced
// here, on the Publisher every server-initiated sound goes through, so every
// current and future sound source is covered without per-feature checks. The
// menu's explicit audio test (/v1/device/audio/test) bypasses it on purpose.
//
// now must return wall-clock local time (time.Now in production); quietActive
// reads Hour()/Minute() directly, no zone conversion.
type quietPublisher struct {
	Publisher // every other method passes through unchanged
	cfg       func() *Config
	now       func() time.Time
}

func (q *quietPublisher) quiet() bool {
	enabled, start, end := q.cfg().quietHoursWindow()
	return enabled && quietActive(start, end, q.now())
}

func (q *quietPublisher) Notify(ctx context.Context, payload map[string]any) error {
	if q.quiet() {
		stripped := make(map[string]any, len(payload))
		for k, v := range payload {
			if slices.Contains(soundKeys, k) {
				continue
			}
			stripped[k] = v
		}
		payload = stripped
	}
	return q.Publisher.Notify(ctx, payload)
}

func (q *quietPublisher) PlayRTTTL(ctx context.Context, rtttl string) error {
	if q.quiet() {
		return nil
	}
	return q.Publisher.PlayRTTTL(ctx, rtttl)
}
