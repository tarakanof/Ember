package main

import (
	"context"
	"time"
)

// quietPublisher gates all device audio behind the quiet-hours window. During
// the window, Notify payloads lose their sound/rtttl keys and the melody
// endpoints succeed without calling the device; everything else delegates
// unchanged. Enforced here — the single path to the device — so every current
// and future sound source is covered without per-feature checks.
//
// now must return wall-clock local time (time.Now in production); quietActive
// reads Hour()/Minute() directly, no zone conversion.
type quietPublisher struct {
	next Publisher
	cfg  func() *Config
	now  func() time.Time
}

func (q *quietPublisher) quiet() bool {
	enabled, start, end := q.cfg().quietHoursWindow()
	return enabled && quietActive(start, end, q.now())
}

func (q *quietPublisher) Notify(ctx context.Context, payload map[string]any) error {
	if q.quiet() {
		stripped := make(map[string]any, len(payload))
		for k, v := range payload {
			if k == "sound" || k == "rtttl" {
				continue
			}
			stripped[k] = v
		}
		payload = stripped
	}
	return q.next.Notify(ctx, payload)
}

func (q *quietPublisher) PlayRTTTL(ctx context.Context, rtttl string) error {
	if q.quiet() {
		return nil
	}
	return q.next.PlayRTTTL(ctx, rtttl)
}

func (q *quietPublisher) PlaySound(ctx context.Context, name string) error {
	if q.quiet() {
		return nil
	}
	return q.next.PlaySound(ctx, name)
}

func (q *quietPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	return q.next.CustomApp(ctx, name, payload)
}
func (q *quietPublisher) ClearApp(ctx context.Context, name string) error {
	return q.next.ClearApp(ctx, name)
}
func (q *quietPublisher) ListApps(ctx context.Context) ([]string, error) { return q.next.ListApps(ctx) }
func (q *quietPublisher) DismissNotify(ctx context.Context) error        { return q.next.DismissNotify(ctx) }
func (q *quietPublisher) Indicator(ctx context.Context, index int, payload map[string]any) error {
	return q.next.Indicator(ctx, index, payload)
}
func (q *quietPublisher) ClearIndicator(ctx context.Context, index int) error {
	return q.next.ClearIndicator(ctx, index)
}
func (q *quietPublisher) Settings(ctx context.Context, payload map[string]any) error {
	return q.next.Settings(ctx, payload)
}
func (q *quietPublisher) Switch(ctx context.Context, name string) error {
	return q.next.Switch(ctx, name)
}
func (q *quietPublisher) ListIcons(ctx context.Context) ([]string, error) {
	return q.next.ListIcons(ctx)
}
func (q *quietPublisher) PutIcon(ctx context.Context, filename string, data []byte) error {
	return q.next.PutIcon(ctx, filename, data)
}
