package main

import "time"

// statsRanges maps a stats endpoint's ?range= to its window and point
// spacing (0 = as stored). Knob and clock stats share them.
var statsRanges = map[string]struct {
	window, bucket time.Duration
}{
	"15m": {15 * time.Minute, 0},
	"1h":  {time.Hour, 0},
	"24h": {24 * time.Hour, 5 * time.Minute},
}

// statsMinuteCap holds 24 h of one-minute buckets; buckets older than
// statsMinuteWindow also leave the ring, so a device reporting every 5 min
// keeps 288, not 5 days.
const (
	statsMinuteCap    = 24 * 60
	statsMinuteWindow = 24 * time.Hour
)

// statsLiveWindow is how far back the live ring (finer than a minute) reaches.
const statsLiveWindow = 10 * time.Minute

// statsSample is a stored reading: its time, and how it folds a newer one
// into a bucket.
type statsSample[T any] interface {
	stamp() time.Time
	merge(newer T) T
}

// sampleRing is a FIFO of at most capacity samples, oldest first. Its
// buffer grows with use up to capacity, so a device costs what it reports.
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
			// Grow (doubling, never past capacity) and put the samples in
			// order: after a dropBefore they may wrap.
			grown := make([]T, r.len, min(r.capacity, max(16, 2*r.len)))
			for i := range r.len {
				grown[i] = *r.at(i)
			}
			r.buf, r.start = grown, 0
		}
		r.buf = append(r.buf, s)
		r.len++
		return
	}
	r.buf[r.start] = s
	r.start = (r.start + 1) % len(r.buf)
}

// dropBefore removes samples older than from at the oldest end.
func (r *sampleRing[T]) dropBefore(from time.Time) {
	var zero T
	for r.len > 0 && (*r.at(0)).stamp().Before(from) {
		*r.at(0) = zero // release its pointers
		r.start = (r.start + 1) % len(r.buf)
		r.len--
	}
}

func (r *sampleRing[T]) last() *T {
	if r.len == 0 {
		return nil
	}
	return r.at(r.len - 1)
}

// since returns copies of the samples at or after from, oldest first.
func (r *sampleRing[T]) since(from time.Time) []T {
	var out []T
	for i := range r.len {
		if s := r.at(i); !(*s).stamp().Before(from) {
			out = append(out, *s)
		}
	}
	return out
}

// sampleSeries is one device's samples: every one in the live ring, and
// folded per minute into the minute ring. Not safe for concurrent use; the
// owning store locks.
type sampleSeries[T statsSample[T]] struct {
	minutes sampleRing[T]
	live    sampleRing[T]
}

func newSampleSeries[T statsSample[T]](liveCap int) sampleSeries[T] {
	return sampleSeries[T]{minutes: newSampleRing[T](statsMinuteCap), live: newSampleRing[T](liveCap)}
}

// record adds s (already stamped): whole to the live ring, folded into the
// current minute's bucket in the minute ring.
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

// points returns the samples in rng's window ending at now, oldest first.
// 15m uses live samples where it has them; 24h folds into 5-minute buckets.
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
