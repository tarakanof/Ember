package main

import (
	"context"
	"slices"
	"time"
)

var soundKeys = []string{"sound", "soundRtttl", "soundLoop"}

type quietPublisher struct {
	Publisher
	cfg func() *Config
	now func() time.Time
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
