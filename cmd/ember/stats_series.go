package main

import "time"

var statsRanges = map[string]struct {
	window, bucket time.Duration
}{
	"15m": {15 * time.Minute, 0},
	"1h":  {time.Hour, 0},
	"24h": {24 * time.Hour, 5 * time.Minute},
}

const (
	statsMinuteCap    = 24 * 60
	statsMinuteWindow = 24 * time.Hour
)

const statsLiveWindow = 10 * time.Minute

type statsSample[T any] interface {
	stamp() time.Time
	merge(newer T) T
}

type sampleRing[T statsSample[T]] struct {
	buf        []T
	capacity   int
	start, len int
}

func newSampleRing[T statsSample[T]](capacity int) sampleRing[T] {
	return sampleRing[T]{capacity: capacity}
}

func (r *sampleRing[T]) at(i int) *T { return &r.buf[(r.start+i)%len(r.buf)] }

func (r *sampleRing[T]) push(s T) {
	if r.len < len(r.buf) {
		*r.at(r.len) = s
		r.len++
		return
	}
	if len(r.buf) < r.capacity {
		if r.start != 0 || len(r.buf) == cap(r.buf) {
			r.resize(min(r.capacity, max(16, 2*r.len)))
		}
		r.buf = append(r.buf, s)
		r.len++
		return
	}
	r.buf[r.start] = s
	r.start = (r.start + 1) % len(r.buf)
}

func (r *sampleRing[T]) dropBefore(from time.Time) {
	var zero T
	for r.len > 0 && (*r.at(0)).stamp().Before(from) {
		*r.at(0) = zero
		r.start = (r.start + 1) % len(r.buf)
		r.len--
	}
	if len(r.buf) > 32 && r.len < len(r.buf)/4 {
		r.resize(max(16, 2*r.len))
	}
}

func (r *sampleRing[T]) resize(n int) {
	grown := make([]T, r.len, n)
	for i := range r.len {
		grown[i] = *r.at(i)
	}
	r.buf, r.start = grown, 0
}

func (r *sampleRing[T]) last() *T {
	if r.len == 0 {
		return nil
	}
	return r.at(r.len - 1)
}

func (r *sampleRing[T]) since(from time.Time) []T {
	var out []T
	for i := range r.len {
		if s := r.at(i); !(*s).stamp().Before(from) {
			out = append(out, *s)
		}
	}
	return out
}

type sampleSeries[T statsSample[T]] struct {
	minutes sampleRing[T]
	live    sampleRing[T]
}

func newSampleSeries[T statsSample[T]](liveCap int) sampleSeries[T] {
	return sampleSeries[T]{minutes: newSampleRing[T](statsMinuteCap), live: newSampleRing[T](liveCap)}
}

func (s *sampleSeries[T]) record(x T) {
	s.live.dropBefore(x.stamp().Add(-statsLiveWindow))
	s.live.push(x)
	if last := s.minutes.last(); last != nil && (*last).stamp().Truncate(time.Minute).Equal(x.stamp().Truncate(time.Minute)) {
		*last = (*last).merge(x)
		return
	}
	s.minutes.dropBefore(x.stamp().Add(-statsMinuteWindow))
	s.minutes.push(x)
}

func (s *sampleSeries[T]) points(rng string, now time.Time) []T {
	r := statsRanges[rng]
	from := now.Add(-r.window)
	minutes := s.minutes.since(from)
	out := make([]T, 0, len(minutes))
	if rng == "15m" {
		live := s.live.since(maxTime(from, now.Add(-statsLiveWindow)))
		cut := now
		if len(live) > 0 {
			cut = live[0].stamp()
		}
		for _, m := range minutes {
			if m.stamp().Before(cut) {
				out = append(out, m)
			}
		}
		return append(out, live...)
	}
	if r.bucket == 0 {
		return append(out, minutes...)
	}
	for _, m := range minutes {
		if n := len(out); n > 0 && out[n-1].stamp().Truncate(r.bucket).Equal(m.stamp().Truncate(r.bucket)) {
			out[n-1] = out[n-1].merge(m)
			continue
		}
		out = append(out, m)
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
