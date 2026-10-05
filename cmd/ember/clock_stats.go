package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// clockStatsLiveCap holds the live window of 30 s probes.
const clockStatsLiveCap = int(statsLiveWindow / clockProbeTTL)

// clockSample is one clock probe, or a bucket of them, and its wire shape
// in GET /v1/clock/stats: units in keys, null when the clock didn't report
// it (unreachable, or no such sensor).
type clockSample struct {
	T time.Time `json:"t"`
	// Reachable: the probe got an answer (any probe in a bucket did).
	Reachable        bool     `json:"reachable"`
	RSSIDBm          *int     `json:"rssi_dbm"`
	FreeHeapBytes    *int64   `json:"free_heap_bytes"`
	MinFreeHeapBytes *int64   `json:"min_free_heap_bytes"`
	TemperatureC     *float64 `json:"temperature_c"`
	HumidityPercent  *float64 `json:"humidity_percent"`
	LightLux         *float64 `json:"light_lux"`
	BatteryPercent   *float64 `json:"battery_percent"`
	// PublishOK and PublishFail count publishes to the clock since the
	// previous sample.
	PublishOK   int64 `json:"publish_ok"`
	PublishFail int64 `json:"publish_fail"`
}

func (s clockSample) stamp() time.Time { return s.T }

// merge folds b (newer) into a: readings take b's where it has them, the
// heap low-water mark the min, publish counts the sum.
func (a clockSample) merge(b clockSample) clockSample {
	out := b
	out.Reachable = a.Reachable || b.Reachable
	out.PublishOK, out.PublishFail = a.PublishOK+b.PublishOK, a.PublishFail+b.PublishFail
	if b.RSSIDBm == nil {
		out.RSSIDBm = a.RSSIDBm
	}
	if b.FreeHeapBytes == nil {
		out.FreeHeapBytes = a.FreeHeapBytes
	}
	if a.MinFreeHeapBytes != nil && (b.MinFreeHeapBytes == nil || *a.MinFreeHeapBytes < *b.MinFreeHeapBytes) {
		out.MinFreeHeapBytes = a.MinFreeHeapBytes
	}
	for _, f := range []struct{ dst, old **float64 }{
		{&out.TemperatureC, &a.TemperatureC}, {&out.HumidityPercent, &a.HumidityPercent},
		{&out.LightLux, &a.LightLux}, {&out.BatteryPercent, &a.BatteryPercent},
	} {
		if *f.dst == nil {
			*f.dst = *f.old
		}
	}
	return out
}

// clockStatsStore keeps the clock's probe samples in memory only: a restart
// loses them, and nothing writes the database for them.
type clockStatsStore struct {
	now func() time.Time

	mu        sync.Mutex // protects everything below
	series    sampleSeries[clockSample]
	latest    *clockSample // newest reachable sample
	last      *clockSample // newest sample
	ip        string
	base      string // the clock URL the samples are from
	okTotal   int64
	failTotal int64
}

func newClockStatsStore() *clockStatsStore {
	return &clockStatsStore{now: time.Now, series: newSampleSeries[clockSample](clockStatsLiveCap)}
}

// resetLocked drops every sample when base is another clock than the
// samples came from, so two devices never share a chart.
func (c *clockStatsStore) resetLocked(base string) {
	if c.base == base {
		return
	}
	if c.base != "" {
		c.series = newSampleSeries[clockSample](clockStatsLiveCap)
		c.latest, c.last, c.ip = nil, nil, ""
	}
	c.base = base
}

// record adds a sample at now from the clock at base.
func (c *clockStatsStore) record(now time.Time, base string, s clockSample) {
	s.T = now.UTC().Truncate(time.Second)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked(base)
	c.series.record(s)
	c.last = &s
	if s.Reachable {
		c.latest = &s
	}
}

// publishDeltas turns the running publish totals into counts since the
// previous call (the first call counts from server start).
func (c *clockStatsStore) publishDeltas(okTotal, failTotal int64) (ok, fail int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok, fail = max(okTotal-c.okTotal, 0), max(failTotal-c.failTotal, 0)
	c.okTotal, c.failTotal = okTotal, failTotal
	return ok, fail
}

func (c *clockStatsStore) minuteLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.series.minutes.len
}

// recordClockProbe stores a fresh probe of the clock at base as a sample.
func (a *App) recordClockProbe(now time.Time, base string, dev clockDeviceOut) {
	ok, fail := a.clockStats.publishDeltas(a.metrics.publishTotalOK.Load(), a.metrics.publishTotalFail.Load())
	s := clockSample{Reachable: dev.Reachable, PublishOK: ok, PublishFail: fail}
	if dev.Reachable {
		s.RSSIDBm, s.FreeHeapBytes, s.MinFreeHeapBytes = dev.WifiRSSIDbm, dev.FreeHeapBytes, dev.MinFreeHeapBytes
		s.TemperatureC, s.HumidityPercent, s.BatteryPercent = dev.TemperatureC, dev.HumidityPercent, dev.BatteryPercent
		s.LightLux = dev.lightLevel
	}
	a.clockStats.record(now, base, s)
	if dev.Reachable && dev.ip != "" {
		a.clockStats.mu.Lock()
		a.clockStats.ip = dev.ip
		a.clockStats.mu.Unlock()
	}
}

// StartClockSampler probes the clock at once and then every interval until
// ctx is done, so its stats fill in without a client asking. It runs only
// when the device watch (whose probe samples too) is off; the probe cache
// serves anyone else asking in between.
func (a *App) StartClockSampler(ctx context.Context, interval time.Duration) {
	a.probeClockHealth(ctx, time.Now())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.probeClockHealth(ctx, now)
		}
	}
}

// clockStatsView is GET /v1/clock/stats.
type clockStatsView struct {
	Range string `json:"range"`
	// Configured: the server has a clock address.
	Configured bool `json:"configured"`
	// Reachable is the newest probe's answer; null before the first.
	Reachable *bool      `json:"reachable"`
	CheckedAt *time.Time `json:"checked_at"`
	// IPAddress is the clock's own report (owner-token only; /v1/clock/health
	// leaves it out).
	IPAddress         *string `json:"ip_address"`
	SampleIntervalSec int     `json:"sample_interval_sec"`
	// Latest is the newest sample that reached the clock.
	Latest *clockSample  `json:"latest"`
	Points []clockSample `json:"points"`
}

func (a *App) buildClockStats(rng string, now time.Time) clockStatsView {
	v := clockStatsView{
		Range:             rng,
		Configured:        a.cfg.Load().effectiveClockURL() != "" && !clockDisabled(),
		SampleIntervalSec: int(clockProbeTTL / time.Second),
	}
	c := a.clockStats
	c.mu.Lock()
	defer c.mu.Unlock()
	if base := a.cfg.Load().effectiveClockURL(); base != "" {
		c.resetLocked(base)
	}
	v.Points = c.series.points(rng, now)
	if c.last != nil {
		v.Reachable = refOf(c.last.Reachable)
		v.CheckedAt = refOf(c.last.T)
	}
	if c.latest != nil {
		v.Latest = refOf(*c.latest)
	}
	v.IPAddress = optString(c.ip)
	return v
}

func (a *App) handleClockStats(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	if _, ok := statsRanges[rng]; !ok {
		a.writeDeviceError(w, r, fmt.Errorf("%w: range must be 15m, 1h or 24h", errDeviceBody))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.buildClockStats(rng, a.clockStats.now()))
}
